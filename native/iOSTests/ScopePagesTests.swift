import XCTest
@testable import Agenthail

@MainActor
final class ScopePagesTests: XCTestCase {
    private var model: AgenthailIOSModel!

    override func setUp() async throws {
        ScopeServer.state.reset()
        KeychainStore.removeAll()
    }

    override func tearDown() async throws {
        for host in model?.hosts.map(\.id) ?? [] { model.forgetHost(host) }
        model = nil
        KeychainStore.removeAll()
    }

    private func connect(_ hosts: [String]) async throws -> AgenthailIOSModel {
        for host in hosts { try AgenthailIOSTests.pair(host, "https://\(host).tailnet.ts.net") }
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [ScopeServer.self]
        model = AgenthailIOSModel(autoConnect: false, session: URLSession(configuration: configuration))
        _ = await model.refresh(fresh: true)
        return model
    }

    private func listed(_ filter: SessionFilter, search: String = "") -> Set<String> {
        Set(model.sessionTree(scope: filter, search: search).projects.flatMap { $0.checkouts.flatMap { $0.families.flatMap { $0.sessions.map(\.id) } } })
    }

    private func catalog(_ type: String, seq: UInt64, _ data: String) throws -> CatalogStreamEvent {
        try JSONDecoder().decode(CatalogStreamEvent.self, from: Data(#"{"stream":"catalog","seq":\#(seq),"type":"\#(type)","data":\#(data)}"#.utf8))
    }

    func testAllIsReadPageByPageFromEveryComputer() async throws {
        ScopeServer.state.set("one", current: [ScopeServer.row("live", name: "fresh")], scopes: ["all": [[ScopeServer.row("live", name: "stale"), ScopeServer.row("a")], [ScopeServer.row("b")]]])
        ScopeServer.state.set("two", current: [], scopes: ["all": [[ScopeServer.row("c")]]])
        let model = try await connect(["one", "two"])
        XCTAssertEqual(listed(.all), ["one::live"])
        XCTAssertTrue(model.canListMore(.all))

        await model.listMore(.all)
        XCTAssertEqual(listed(.all), ["one::live", "one::a", "two::c"])
        XCTAssertEqual(model.snapshot?.sessions.first { $0.id == "one::live" }?.name, "fresh", "a page's copy replaced the current one")
        XCTAssertTrue(model.canListMore(.all))
        await model.listMore(.all)
        XCTAssertEqual(listed(.all), ["one::live", "one::a", "one::b", "two::c"])
        XCTAssertFalse(model.canListMore(.all))
        XCTAssertTrue(ScopeServer.state.requests("one").contains { $0.contains("scope=all") && $0.contains("cursor=1") })
        XCTAssertEqual(ScopeServer.state.requests("two").filter { $0.contains("scope=all") }.count, 1, "a finished computer was read again")
        XCTAssertEqual(model.snapshot?.sessions.filter { $0.id == "one::live" }.count, 1)
    }

    func testAnArchivedSessionLeavesTheLoadedPages() async throws {
        ScopeServer.state.set("one", current: [ScopeServer.row("live")], scopes: ["all": [[ScopeServer.row("live"), ScopeServer.row("old"), ScopeServer.row("kept")]]])
        let model = try await connect(["one"])
        await model.listMore(.all)
        XCTAssertEqual(listed(.all), ["one::live", "one::old", "one::kept"])
        ScopeServer.state.set("one", current: [], scopes: ["all": [[ScopeServer.row("kept")]]])

        let host = try XCTUnwrap(model.connection("one"))
        await host.receiveCatalog([try catalog("session.removed", seq: 500, #"{"sessionId":"live","reason":"unlisted"}"#), try catalog("session.removed", seq: 501, #"{"sessionId":"old","reason":"unlisted"}"#)])
        XCTAssertEqual(listed(.all), ["one::kept"], "an archived session stayed listed from a loaded page")
        XCTAssertEqual(listed(.recent), [])
        for _ in 0..<100 where !ScopeServer.state.requests("one").contains(where: { $0.contains("scope=all") && $0.contains("limit=200") }) { try await Task.sleep(for: .milliseconds(20)) }
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertEqual(listed(.all), ["one::kept"])

        await host.receiveCatalog([try catalog("session.removed", seq: 502, #"{"sessionId":"kept"}"#)])
        XCTAssertEqual(listed(.all), [], "a removed session stayed listed from a loaded page")
    }

    func testAnOlderSearchAnswerDoesNotReplaceANewerOne() async throws {
        ScopeServer.state.set("one", current: [], scopes: [:], searches: ["ze": ([ScopeServer.row("slow", name: "zeal")], .milliseconds(800)), "zet": ([ScopeServer.row("fast", name: "zeta")], .zero)])
        let model = try await connect(["one"])
        let older = Task { await model.searchScope("ze", filter: .all) }
        try await Task.sleep(for: .milliseconds(100))
        await model.searchScope("zet", filter: .all)
        XCTAssertEqual(listed(.all, search: "zet"), ["one::fast"])
        _ = await older.value
        XCTAssertEqual(model.connection("one")?.pages.found.map(\.id), ["fast"], "an older search answer replaced the newer one")
        XCTAssertTrue(ScopeServer.state.requests("one").contains { $0.contains("scope=all") && $0.contains("q=zet") })

        await model.searchScope("", filter: .all)
        XCTAssertEqual(model.connection("one")?.pages.found.count, 0)
    }

    func testAnUnreachableComputerKeepsItsRowsAndResumesItsCursor() async throws {
        ScopeServer.state.set("one", current: [], scopes: ["all": [[ScopeServer.row("a")], [ScopeServer.row("b")]]])
        let model = try await connect(["one"])
        await model.listMore(.all)
        let host = try XCTUnwrap(model.connection("one"))
        host.recordStreamInterruption(probeError: AgenthailAPIError.unavailable("offline"))

        let row = try XCTUnwrap(model.snapshot?.sessions.first { $0.id == "one::a" })
        XCTAssertEqual(row.host?.state, "unreachable")
        XCTAssertTrue(row.unavailableReason?.hasSuffix("can't be reached") == true)
        let before = ScopeServer.state.requests("one").count
        await model.listMore(.all)
        XCTAssertEqual(ScopeServer.state.requests("one").count, before, "an unreachable computer was asked for a page")

        _ = await host.refresh(fresh: true)
        await model.listMore(.all)
        XCTAssertTrue(ScopeServer.state.requests("one").last?.contains("cursor=1") == true, "the next page did not resume from the held cursor")
        XCTAssertEqual(listed(.all), ["one::a", "one::b"])
    }

    func testAMentionFindsASessionOutsideTheCurrentOnes() async throws {
        var named = ScopeServer.row("helper", name: "zeta")
        named["alias"] = "zeta"
        ScopeServer.state.set("one", current: [ScopeServer.row("live")], scopes: [:], searches: ["zeta": ([named], .zero)])
        let model = try await connect(["one"])
        let found = await model.mentionSessions(for: "one::live", query: "zeta")
        XCTAssertEqual(found?.map(\.id), ["one::helper"])
        XCTAssertTrue(ScopeServer.state.requests("one").contains { $0.contains("scope=all") && $0.contains("q=zeta") && $0.contains("limit=20") })
    }
}

final class ScopeServer: URLProtocol, @unchecked Sendable {
    final class State: @unchecked Sendable {
        struct Host {
            var current: [[String: Any]]
            var scopes: [String: [[[String: Any]]]]
            var searches: [String: (rows: [[String: Any]], delay: Duration)]
            var requests: [String] = []
        }

        private let lock = NSLock()
        private var hosts: [String: Host] = [:]

        func reset() { lock.withLock { hosts = [:] } }
        func set(_ host: String, current: [[String: Any]], scopes: [String: [[[String: Any]]]], searches: [String: (rows: [[String: Any]], delay: Duration)] = [:]) {
            lock.withLock {
                let requests = hosts[host]?.requests ?? []
                hosts[host] = Host(current: current, scopes: scopes, searches: searches, requests: requests)
            }
        }
        func requests(_ host: String) -> [String] { lock.withLock { hosts[host]?.requests ?? [] } }
        func record(_ host: String, _ path: String) -> Host? {
            lock.withLock {
                hosts[host]?.requests.append(path)
                return hosts[host]
            }
        }
    }

    static let state = State()

    static func row(_ id: String, name: String? = nil) -> [String: Any] {
        var row = PartsServer.row(id)
        row["name"] = name ?? id
        row["current"] = false
        return row
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let url = request.url!
        let name = String(url.host!.split(separator: ".")[0])
        guard let host = Self.state.record(name, url.path + (url.query.map { "?" + $0 } ?? "")) else { return respond(503, "{}") }
        let query = Dictionary((URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []).map { ($0.name, $0.value ?? "") }, uniquingKeysWith: { first, _ in first })
        switch url.path {
        case "/api/v1/version":
            respond(200, #"{"protocol":2,"minimumProtocol":1,"maximumProtocol":2}"#)
        case "/api/v1/projects/activity":
            respond(200, #"{"projects":[]}"#)
        case "/api/v1/snapshot" where query["sessions"] == "none":
            var base = try! JSONSerialization.jsonObject(with: Data(SessionPreview.snapshotJSON.utf8)) as! [String: Any]
            base.removeValue(forKey: "sessions")
            base["hostEpoch"] = "epoch"
            base["catalogSeq"] = 1
            respond(200, Self.json(base))
        case "/api/v1/snapshot" where query["scope"] == "current":
            respond(200, Self.json(["sessions": host.current.map { var row = $0; row["current"] = true; return row }, "hostEpoch": "epoch", "catalogSeq": 1]))
        case "/api/v1/snapshot" where query["q"] != nil:
            let search = host.searches[query["q"]!] ?? ([], .zero)
            let body = Self.json(["sessions": search.rows])
            let delay = search.delay.components
            let seconds = Double(delay.seconds) + Double(delay.attoseconds) / 1e18
            if seconds == 0 { return respond(200, body) }
            DispatchQueue.global().asyncAfter(deadline: .now() + seconds) { self.respond(200, body) }
        case "/api/v1/snapshot" where query["scope"] != nil:
            let pages = host.scopes[query["scope"]!] ?? [[]]
            let index = query["cursor"].flatMap(Int.init) ?? 0
            var page: [String: Any] = ["sessions": index < pages.count ? pages[index] : []]
            if index + 1 < pages.count { page["nextCursor"] = String(index + 1) }
            respond(200, Self.json(page))
        default:
            respond(200, "{}")
        }
    }

    override func stopLoading() {}

    private static func json(_ object: Any) -> String { String(data: try! JSONSerialization.data(withJSONObject: object), encoding: .utf8)! }

    private func respond(_ status: Int, _ body: String) {
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
}
