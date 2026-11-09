import XCTest
@testable import Agenthail

@MainActor
final class SnapshotPartsTests: XCTestCase {
    override func setUp() {
        PartsServer.state.reset()
    }

    private func connection(_ hostID: String = "parts") -> HostConnection {
        let credential = PairedHostCredential(hostID: hostID, hostName: hostID, endpoint: URL(string: "https://\(hostID).tailnet.ts.net")!, token: "fixture", pairedAt: Date())
        return HostConnection(credential: credential, session: FixtureScope.open(self).session([PartsServer.self]))
    }

    func testAHostSnapshotIsItsBaseWithCurrentSessionsAndTheirSubagents() async throws {
        PartsServer.state.set(host: "parts", scopes: [[PartsServer.row("root"), PartsServer.row("child", parent: "root")]], baseSeq: 10, scopeSeq: 7)
        let host = connection()
        let refreshed = await host.refresh(fresh: true)
        XCTAssertTrue(refreshed)
        XCTAssertEqual(Set(host.snapshot?.sessions.map(\.id) ?? []), ["root", "child"], "the current scope carries its subagents")
        XCTAssertEqual(host.catalogStreamCursor, 7, "the stream resumes from the older read")
        let paths = PartsServer.state.requests(for: "parts")
        XCTAssertTrue(paths.contains { $0.contains("sessions=none") })
        XCTAssertTrue(paths.contains { $0.contains("scope=current") && $0.contains("members=1") })
        XCTAssertTrue(paths.contains { $0.hasPrefix("/api/v1/projects/activity") })
        XCTAssertFalse(paths.contains { $0 == "/api/v1/snapshot" || $0 == "/api/v1/snapshot?fresh=1" }, "the phone asked for the full snapshot")
    }

    func testAnUnchangedBaseKeepsItsLastState() async throws {
        PartsServer.state.set(host: "parts", scopes: [[PartsServer.row("root")]], baseSeq: 3, scopeSeq: 3)
        let host = connection()
        _ = await host.refresh()
        let first = host.snapshot
        XCTAssertNotNil(first)
        _ = await host.refresh()
        XCTAssertTrue(PartsServer.state.sentNotModified(for: "parts"), "the second base read named the first one's tag")
        XCTAssertEqual(host.snapshot?.queue, first?.queue)
        XCTAssertEqual(host.snapshot?.daemon, first?.daemon)
        XCTAssertEqual(host.snapshot?.sessions.map(\.id), ["root"])
    }

    private func olderRefreshHeld(_ host: HostConnection) async throws -> Task<Bool, Never> {
        let older = Task { await host.refresh(fresh: true) }
        for _ in 0..<500 where !PartsServer.state.holdingScope("parts") { try await Task.sleep(for: .milliseconds(10)) }
        XCTAssertTrue(PartsServer.state.holdingScope("parts"), "the older refresh never reached the computer")
        return older
    }

    func testALateOlderRefreshDoesNotReplaceANewerOne() async throws {
        PartsServer.state.set(host: "parts", scopes: [[PartsServer.row("older")], [PartsServer.row("newer")]], baseSeq: 1, scopeSeq: 1, holdsFirstScope: true)
        let host = connection()
        let older = try await olderRefreshHeld(host)
        _ = await host.refresh(fresh: true)
        XCTAssertEqual(host.snapshot?.sessions.map(\.id), ["newer"])
        PartsServer.state.releaseScope("parts")
        _ = await older.value
        XCTAssertEqual(host.snapshot?.sessions.map(\.id), ["newer"], "a slower, older refresh replaced the newer state")
    }

    func testALateOlderFailureDoesNotShowAnErrorOverANewerSuccess() async throws {
        PartsServer.state.set(host: "parts", scopes: [[PartsServer.row("older")], [PartsServer.row("newer")]], baseSeq: 1, scopeSeq: 1, holdsFirstScope: true, failingScopeReads: [0])
        let host = connection()
        let older = try await olderRefreshHeld(host)
        let refreshed = await host.refresh(fresh: true)
        XCTAssertTrue(refreshed)
        XCTAssertNil(host.connectionError)
        PartsServer.state.releaseScope("parts")
        let failed = await older.value
        XCTAssertFalse(failed)
        XCTAssertNil(host.connectionError, "a slower, older refresh that failed showed its error over the newer success")
        XCTAssertEqual(host.snapshot?.sessions.map(\.id), ["newer"])
    }

    func testALateOlderSuccessShowsItsStateButKeepsTheNewerFailure() async throws {
        PartsServer.state.set(host: "parts", scopes: [[PartsServer.row("older")], [PartsServer.row("newer")]], baseSeq: 1, scopeSeq: 1, holdsFirstScope: true, failingScopeReads: [1])
        let host = connection()
        let older = try await olderRefreshHeld(host)
        let refreshed = await host.refresh(fresh: true)
        XCTAssertFalse(refreshed)
        XCTAssertNotNil(host.connectionError)
        PartsServer.state.releaseScope("parts")
        let succeeded = await older.value
        XCTAssertTrue(succeeded)
        XCTAssertEqual(host.snapshot?.sessions.map(\.id), ["older"], "a snapshot newer than any shown was dropped")
        XCTAssertNotNil(host.connectionError, "an older success cleared the newer failure")
    }

    func testAComputerRolledBackToAnOlderAgenthailStopsRetryingItsStream() async throws {
        PartsServer.state.set(host: "parts", scopes: [[PartsServer.row("root")]], baseSeq: 1, scopeSeq: 1)
        let host = connection()
        defer { host.stop() }
        host.connect()
        for _ in 0..<200 where host.snapshot == nil { try await Task.sleep(for: .milliseconds(20)) }
        XCTAssertNotNil(host.snapshot)
        PartsServer.state.speak("parts", protocol: 1)
        for _ in 0..<300 where host.connectionError == nil || host.reconnecting { try await Task.sleep(for: .milliseconds(20)) }
        XCTAssertEqual(host.connectionError, "Update Agenthail on parts to connect.")
        XCTAssertFalse(host.reconnecting)
        let probes = PartsServer.state.requests(for: "parts").filter { $0 == "/api/v1/version" }.count
        try await Task.sleep(for: .seconds(1.5))
        XCTAssertEqual(PartsServer.state.requests(for: "parts").filter { $0 == "/api/v1/version" }.count, probes, "the phone kept probing a computer it cannot use")
    }

    func testASessionOutsideTheCurrentScopeOpensFromItsOwnerByItsID() async throws {
        KeychainStore.removeAll()
        defer { KeychainStore.removeAll() }
        try AgenthailIOSTests.pair("parts", "https://parts.tailnet.ts.net")
        PartsServer.state.set(host: "parts", scopes: [[PartsServer.row("root")]], baseSeq: 1, scopeSeq: 1)
        let model = AgenthailIOSModel(autoConnect: false, session: FixtureScope.open(self).session([PartsServer.self]))
        defer { model.forgetHost("parts") }
        _ = await model.refresh(fresh: true)
        XCTAssertEqual(model.snapshot?.sessions.map(\.id), ["parts::root"])
        let opened = try XCTUnwrap(IOSAppDelegate.openedSession(["sessionId": "elsewhere", "host": "parts"]))
        model.openSession(opened)
        await model.loadSession(try XCTUnwrap(model.requestedSessionID))
        XCTAssertTrue(PartsServer.state.requests(for: "parts").contains { $0.hasPrefix("/api/v1/session?") && $0.contains("id=elsewhere") }, "a notification's session was not asked of its owner: \(PartsServer.state.requests(for: "parts"))")
    }

    func testASlowComputerDoesNotHoldTheOthers() async throws {
        KeychainStore.removeAll()
        defer { KeychainStore.removeAll() }
        try AgenthailIOSTests.pair("fast", "https://fast.tailnet.ts.net")
        try AgenthailIOSTests.pair("slow", "https://slow.tailnet.ts.net")
        PartsServer.state.set(host: "fast", scopes: [[PartsServer.row("fast-session")]], baseSeq: 1, scopeSeq: 1)
        PartsServer.state.set(host: "slow", scopes: [[PartsServer.row("slow-session")]], baseSeq: 1, scopeSeq: 1, firstScopeDelay: .seconds(4))
        let model = AgenthailIOSModel(autoConnect: false, session: FixtureScope.open(self).session([PartsServer.self]))
        model.hostRefreshWait = .milliseconds(500)
        let clock = ContinuousClock()
        let started = clock.now
        _ = await model.refresh(fresh: true)
        XCTAssertLessThan(clock.now - started, .seconds(2), "the refresh waited for the slow computer")
        XCTAssertEqual(model.connection("fast")?.snapshot?.sessions.map(\.id), ["fast-session"])
        XCTAssertNil(model.connection("slow")?.snapshot, "the slow computer answered early")
        for _ in 0..<300 where model.connection("slow")?.snapshot == nil { try await Task.sleep(for: .milliseconds(20)) }
        XCTAssertEqual(model.connection("slow")?.snapshot?.sessions.map(\.id), ["slow-session"], "the slow computer's refresh never landed")
        model.forgetHost("fast")
        model.forgetHost("slow")
    }

    func testTheCurrentScopeIsWalkedAcrossPagesKeepingEachSessionOnce() async throws {
        PartsServer.state.set(host: "parts", scopes: [[]], baseSeq: 4, scopeSeq: 4)
        PartsServer.state.page("parts", [[PartsServer.row("a"), PartsServer.row("b")], [PartsServer.row("b"), PartsServer.row("c")]])
        let host = connection()
        _ = await host.refresh(fresh: true)
        XCTAssertEqual(host.snapshot?.sessions.map(\.id), ["a", "b", "c"])
        XCTAssertTrue(PartsServer.state.requests(for: "parts").contains { $0.contains("scope=current") && $0.contains("cursor=1") })
    }
}

final class PartsServer: URLProtocol, @unchecked Sendable {
    final class State: @unchecked Sendable {
        struct Host {
            var scopes: [[[String: Any]]]
            var baseSeq: UInt64
            var scopeSeq: UInt64
            var firstScopeDelay: Duration
            var holdsFirstScope = false
            var failingScopeReads: Set<Int> = []
            var heldScope: (() -> Void)? = nil
            var scopeReads = 0
            var requests: [String] = []
            var notModified = false
            var protocolVersion = 2
            var pages: [[[String: Any]]]? = nil
        }

        private let lock = NSLock()
        private var hosts: [String: Host] = [:]

        func reset() { lock.withLock { hosts = [:] } }
        func set(host: String, scopes: [[[String: Any]]], baseSeq: UInt64, scopeSeq: UInt64, firstScopeDelay: Duration = .zero, holdsFirstScope: Bool = false, failingScopeReads: Set<Int> = []) {
            lock.withLock { hosts[host] = Host(scopes: scopes, baseSeq: baseSeq, scopeSeq: scopeSeq, firstScopeDelay: firstScopeDelay, holdsFirstScope: holdsFirstScope, failingScopeReads: failingScopeReads) }
        }
        func holdingScope(_ host: String) -> Bool { lock.withLock { hosts[host]?.heldScope != nil } }
        func hold(_ host: String, _ answer: @escaping () -> Void) { lock.withLock { hosts[host]?.heldScope = answer } }
        func releaseScope(_ host: String) {
            let answer = lock.withLock { () -> (() -> Void)? in
                defer { hosts[host]?.heldScope = nil }
                return hosts[host]?.heldScope
            }
            answer?()
        }
        func requests(for host: String) -> [String] { lock.withLock { hosts[host]?.requests ?? [] } }
        func sentNotModified(for host: String) -> Bool { lock.withLock { hosts[host]?.notModified ?? false } }
        func record(_ host: String, _ path: String) { lock.withLock { hosts[host]?.requests.append(path) } }
        func markNotModified(_ host: String) { lock.withLock { hosts[host]?.notModified = true } }
        func speak(_ host: String, protocol version: Int) { lock.withLock { hosts[host]?.protocolVersion = version } }
        func page(_ host: String, _ pages: [[[String: Any]]]) { lock.withLock { hosts[host]?.pages = pages } }
        func host(_ name: String) -> Host? { lock.withLock { hosts[name] } }
        func nextScope(_ name: String) -> (rows: [[String: Any]], delay: Duration, seq: UInt64, fails: Bool, holds: Bool)? {
            lock.withLock {
                guard var host = hosts[name] else { return nil }
                let read = host.scopeReads
                let index = min(read, host.scopes.count - 1)
                host.scopeReads += 1
                hosts[name] = host
                return (host.scopes[index], read == 0 ? host.firstScopeDelay : .zero, host.scopeSeq, host.failingScopeReads.contains(read), read == 0 && host.holdsFirstScope)
            }
        }
    }

    static let state = State()

    static func row(_ id: String, parent: String? = nil) -> [String: Any] {
        let snapshot = try! JSONSerialization.jsonObject(with: Data(SessionPreview.snapshotJSON.utf8)) as! [String: Any]
        var row = (snapshot["sessions"] as! [[String: Any]])[0]
        row["id"] = id
        row.removeValue(forKey: "subagent")
        if let parent { row["subagent"] = ["parentId": parent, "rootId": parent, "depth": 1] }
        return row
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() { served(answer) }

    private func answer() {
        let url = request.url!
        let name = String(url.host!.split(separator: ".")[0])
        let pathAndQuery = url.path + (url.query.map { "?" + $0 } ?? "")
        Self.state.record(name, pathAndQuery)
        guard let host = Self.state.host(name) else { return respond(503, #"{"error":"unknown host"}"#) }
        switch url.path {
        case "/api/v1/version":
            let version = host.protocolVersion
            respond(200, #"{"protocol":\#(version),"minimumProtocol":1,"maximumProtocol":\#(version)}"#)
        case "/api/v1/events":
            client?.urlProtocol(self, didReceive: HTTPURLResponse(url: url, statusCode: 200, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "text/event-stream"])!, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: Data(": connected\n\n".utf8))
            client?.urlProtocolDidFinishLoading(self)
        case "/api/v1/projects/activity":
            respond(200, #"{"projects":[]}"#)
        case "/api/v1/snapshot" where url.query?.contains("sessions=none") == true:
            if request.value(forHTTPHeaderField: "If-None-Match") == "\"base\"" {
                Self.state.markNotModified(name)
                return respond(304, "", tag: "\"base\"")
            }
            var base = try! JSONSerialization.jsonObject(with: Data(SessionPreview.snapshotJSON.utf8)) as! [String: Any]
            base.removeValue(forKey: "sessions")
            base.removeValue(forKey: "totalSessions")
            base.removeValue(forKey: "history")
            base["hostEpoch"] = "epoch"
            base["catalogSeq"] = host.baseSeq
            respond(200, String(data: try! JSONSerialization.data(withJSONObject: base), encoding: .utf8)!, tag: "\"base\"")
        case "/api/v1/snapshot" where url.query?.contains("scope=current") == true && host.pages != nil:
            let pages = host.pages!
            let cursor = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "cursor" }?.value
            let index = cursor.flatMap(Int.init) ?? 0
            var page: [String: Any] = ["sessions": pages[index], "hostEpoch": "epoch", "catalogSeq": host.scopeSeq, "totalSessions": 3]
            if index + 1 < pages.count { page["nextCursor"] = String(index + 1) }
            respond(200, String(data: try! JSONSerialization.data(withJSONObject: page), encoding: .utf8)!)
        case "/api/v1/snapshot" where url.query?.contains("scope=current") == true:
            guard let scope = Self.state.nextScope(name) else { return respond(503, "{}") }
            let body = String(data: try! JSONSerialization.data(withJSONObject: ["sessions": scope.rows, "hostEpoch": "epoch", "catalogSeq": scope.seq, "totalSessions": scope.rows.count]), encoding: .utf8)!
            let (status, reply) = scope.fails ? (500, #"{"error":{"message":"scope failed"}}"#) : (200, body)
            if scope.holds { return Self.state.hold(name) { self.served { self.respond(status, reply) } } }
            let delay = scope.delay.components
            let seconds = Double(delay.seconds) + Double(delay.attoseconds) / 1e18
            if seconds == 0 { return respond(status, reply) }
            later(seconds) { self.respond(status, reply) }
        default:
            respond(200, "{}")
        }
    }

    override func stopLoading() {}

    private func respond(_ status: Int, _ body: String, tag: String? = nil) {
        var headers = ["Content-Type": "application/json"]
        if let tag { headers["ETag"] = tag }
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1", headerFields: headers)!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
}
