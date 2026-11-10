import XCTest
@testable import Agenthail

@MainActor
final class ProjectBroadcastWalkTests: XCTestCase {
    private static let repo = "repo:github.com/acme/widget"

    private func model(paired: [(String, String)], peers: [(String, String, String)] = [], replies: [String: String]) throws -> AgenthailIOSModel {
        KeychainStore.removeAll()
        WalkProtocol.state.reset(replies)
        for (id, name) in paired { try AgenthailIOSTests.pair(id, "https://\(name).invalid", name: name) }
        let configuration = FixtureScope.open(self).configuration([WalkProtocol.self])
        let model = AgenthailIOSModel(autoConnect: false, session: URLSession(configuration: configuration))
        for (id, _) in paired {
            var snapshot = HostCatalogTests.snapshot([])
            snapshot.daemonHosts(peers.map { HostHealth(id: $0.0, name: $0.1, state: $0.2) })
            model.setHostSnapshot(snapshot, host: id)
        }
        return model
    }

    private func project(servers: [String]) -> ListedProject {
        ListedProject(state: ProjectState(id: Self.repo, displayName: "widget"), hostID: servers[0], servers: servers)
    }

    private static func reply(_ session: String, legs: String = "", echo: String? = nil) -> String {
        let echoed = echo.map { #","exceptHosts":\#($0)"# } ?? ""
        return #"{"recipients":[{"sessionId":"\#(session)","outcome":"delivered"}],"hosts":[\#(legs)],"unreachable":[]\#(echoed)}"#
    }

    func testTwoComputersThatAreNotPeersEachGetTheMessageOnce() async throws {
        let model = try model(paired: [("hlaptop", "laptop"), ("hserver", "server")],
                              replies: ["laptop.invalid": Self.reply("a1"), "server.invalid": Self.reply("b1", echo: #"["hlaptop"]"#)])
        defer { KeychainStore.removeAll() }
        let sent = try await model.broadcast(to: project(servers: ["hlaptop", "hserver"]), message: "ship it", idempotencyKey: "k")
        let sends = WalkProtocol.state.sends
        XCTAssertEqual(sends.map(\.host), ["laptop.invalid", "server.invalid"])
        XCTAssertEqual(sends.map(\.except), [[], ["hlaptop"]])
        XCTAssertEqual(sends.map(\.key), ["k", "k:hserver"], "each computer gets its own retry key")
        XCTAssertEqual(sent, "Sent to 2: a1, b1.")
    }

    func testAComputerTheFirstAlreadyReachedIsNotSentAgain() async throws {
        let leg = #"{"id":"hserver","name":"server","state":"reached","recipients":1}"#
        let model = try model(paired: [("hlaptop", "laptop"), ("hserver", "server")], replies: ["laptop.invalid": Self.reply("a1", legs: leg)])
        defer { KeychainStore.removeAll() }
        _ = try await model.broadcast(to: project(servers: ["hlaptop", "hserver"]), message: "ship it", idempotencyKey: "k")
        XCTAssertEqual(WalkProtocol.state.sends.map(\.host), ["laptop.invalid"])
    }

    func testAPeerReachedByOneComputerIsExceptedOnTheNext() async throws {
        let leg = #"{"id":"hshared","name":"shared","state":"held","recipients":0}"#
        let model = try model(paired: [("hlaptop", "laptop"), ("hserver", "server")],
                              replies: ["laptop.invalid": Self.reply("a1", legs: leg), "server.invalid": Self.reply("b1", echo: #"["hlaptop","hshared"]"#)])
        defer { KeychainStore.removeAll() }
        _ = try await model.broadcast(to: project(servers: ["hlaptop", "hserver"]), message: "ship it", idempotencyKey: "k")
        XCTAssertEqual(WalkProtocol.state.sends.last?.except, ["hlaptop", "hshared"])
    }

    func testAComputerHeldByAPeerIsNamedAsThePhoneNamesIt() async throws {
        let leg = #"{"id":"hserver","name":"build-box.internal.example","state":"held","recipients":0}"#
        let model = try model(paired: [("hlaptop", "laptop"), ("hserver", "server")], replies: ["laptop.invalid": Self.reply("a1", legs: leg)])
        defer { KeychainStore.removeAll() }
        let sent = try await model.broadcast(to: project(servers: ["hlaptop", "hserver"]), message: "ship it", idempotencyKey: "k")
        XCTAssertEqual(sent, "Sent to 1: a1. Held for server until it answers.")
    }

    func testAnOlderComputerThatIgnoredTheExceptionsIsNamed() async throws {
        let shared = #"{"id":"hshared","name":"shared","state":"reached","recipients":1}"#
        let model = try model(paired: [("hlaptop", "laptop"), ("hserver", "server")],
                              replies: ["laptop.invalid": Self.reply("a1", legs: shared), "server.invalid": Self.reply("b1", legs: shared)])
        defer { KeychainStore.removeAll() }
        let sent = try await model.broadcast(to: project(servers: ["hlaptop", "hserver"]), message: "ship it", idempotencyKey: "k")
        XCTAssertTrue(sent.hasSuffix("server runs an older Agenthail; shared may have received this twice."), sent)
    }

    func testAComputerNobodyReachedIsNamed() async throws {
        let model = try model(paired: [("hlaptop", "laptop")], peers: [("hserver", "server", "unreachable")], replies: ["laptop.invalid": Self.reply("a1")])
        defer { KeychainStore.removeAll() }
        let sent = try await model.broadcast(to: project(servers: ["hlaptop", "hserver"]), message: "ship it", idempotencyKey: "k")
        XCTAssertEqual(sent, "Sent to 1: a1. Couldn't reach server.")
    }

    func testOneComputerSendsAsBefore() async throws {
        let model = try model(paired: [("hlaptop", "laptop")], replies: ["laptop.invalid": Self.reply("a1")])
        defer { KeychainStore.removeAll() }
        _ = try await model.broadcast(to: project(servers: ["hlaptop"]), message: "ship it", idempotencyKey: "k")
        XCTAssertEqual(WalkProtocol.state.sends.map(\.except), [[]])
        XCTAssertEqual(WalkProtocol.state.sends.map(\.key), ["k"])
    }
}

private final class WalkProtocol: URLProtocol, @unchecked Sendable {
    struct Send: Equatable { let host: String; let except: [String]; let key: String? }
    final class State: @unchecked Sendable {
        private let lock = NSLock()
        private var recorded: [Send] = []
        private var replies: [String: String] = [:]
        var sends: [Send] { lock.withLock { recorded } }
        func reset(_ replies: [String: String]) { lock.withLock { recorded = []; self.replies = replies } }
        func record(_ send: Send) -> String? { lock.withLock { recorded.append(send); return replies[send.host] } }
    }
    static let state = State()
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() { served(answer) }

    private func answer() {
        var data = request.httpBody ?? Data()
        if let stream = request.httpBodyStream {
            stream.open()
            defer { stream.close() }
            var buffer = [UInt8](repeating: 0, count: 4096)
            while stream.hasBytesAvailable {
                let count = stream.read(&buffer, maxLength: buffer.count)
                if count <= 0 { break }
                data.append(contentsOf: buffer.prefix(count))
            }
        }
        let body = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:]
        var status = 503
        var text = #"{"error":{"message":"unavailable"}}"#
        if request.url?.path == "/api/v1/actions", body["action"] as? String == "channel-send",
           let reply = Self.state.record(Send(host: request.url?.host ?? "", except: body["exceptHosts"] as? [String] ?? [], key: request.value(forHTTPHeaderField: "Idempotency-Key"))) {
            (status, text) = (200, reply)
        }
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(text.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}
