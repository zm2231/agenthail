import XCTest
@testable import Agenthail

@MainActor
final class ProjectBroadcastTests: XCTestCase {
    func testAProjectMessageGoesToItsChannelOnItsComputerAndNamesWhoGotIt() async throws {
        ChannelSendProtocol.state.reset()
        let configuration = FixtureScope.open(self).configuration([ChannelSendProtocol.self])
        let api = AgenthailAPI(baseURL: URL(string: "https://studio.invalid")!, token: "test", session: URLSession(configuration: configuration))
        let model = AgenthailIOSModel(api: api, paired: true, hostID: "hstudio", hostName: "studio")
        model.setHostSnapshot(HostCatalogTests.snapshot([HostCatalogTests.row("s1", name: "lead"), HostCatalogTests.row("s2", name: "tests")]))
        let project = ListedProject(state: ProjectState(id: "widget", displayName: "widget"), hostID: "hstudio")

        let sent = try await model.broadcast(to: project, message: "ship it", idempotencyKey: "k1")

        let request = try XCTUnwrap(ChannelSendProtocol.state.sends.first)
        XCTAssertEqual(request.channel, "#widget")
        XCTAssertEqual(request.message, "ship it")
        XCTAssertEqual(request.key, "k1")
        XCTAssertEqual(sent, "Sent to 2: @lead, tests. Not delivered to s3: closed.")
    }

    func testAComputerThatCannotBeReachedSendsNothing() async {
        let model = AgenthailIOSModel(autoConnect: false)
        let project = ListedProject(state: ProjectState(id: "widget", displayName: "widget"), hostID: "hgone")
        do {
            _ = try await model.broadcast(to: project, message: "ship it", idempotencyKey: "k2")
            XCTFail("a project on an unpaired computer was sent to")
        } catch {
            XCTAssertEqual(AgenthailAPIError.userMessage(error), "Connect to the computer that holds widget first.")
        }
    }
}

private final class ChannelSendProtocol: URLProtocol, @unchecked Sendable {
    struct Send {
        let channel: String
        let message: String
        let key: String?
    }

    final class State: @unchecked Sendable {
        private let lock = NSLock()
        private var recorded: [Send] = []
        var sends: [Send] { lock.withLock { recorded } }
        func reset() { lock.withLock { recorded = [] } }
        func record(_ send: Send) { lock.withLock { recorded.append(send) } }
    }

    static let state = State()
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() { served(answer) }

    private func answer() {
        if request.url?.path == "/api/v1/actions", let body = request.bodyData,
           let json = try? JSONSerialization.jsonObject(with: body) as? [String: Any], json["action"] as? String == "channel-send" {
            Self.state.record(Send(channel: json["channel"] as? String ?? "", message: json["message"] as? String ?? "", key: request.value(forHTTPHeaderField: "Idempotency-Key")))
            respond(200, #"{"recipients":[{"sessionId":"s1","coordinator":"@lead","outcome":"delivered"},{"sessionId":"s2","outcome":"queued"},{"sessionId":"s3","outcome":"failed","detail":"closed"}]}"#)
        } else {
            respond(404, #"{"error":"not found"}"#)
        }
    }

    override func stopLoading() {}

    private func respond(_ status: Int, _ body: String) {
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
}

private extension URLRequest {
    var bodyData: Data? {
        if let httpBody { return httpBody }
        guard let stream = httpBodyStream else { return nil }
        stream.open()
        defer { stream.close() }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable {
            let count = stream.read(&buffer, maxLength: buffer.count)
            guard count > 0 else { break }
            data.append(buffer, count: count)
        }
        return data
    }
}
