import XCTest
@testable import Agenthail

final class StaleConnectionTests: XCTestCase {
    @MainActor
    func testStaleCatalogStillConnectsToEventsAndRefreshes() async throws {
        KeychainStore.removeAll()
        try AgenthailIOSTests.pair("hstale", "https://stale.tailnet.ts.net", token: "fixture")
        StaleConnectionProtocol.state.reset()
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [StaleConnectionProtocol.self]
        let model = AgenthailIOSModel(autoConnect: false, session: URLSession(configuration: configuration))
        defer { model.forgetHost("hstale"); KeychainStore.removeAll() }
        model.connect()
        for _ in 0..<150 where model.snapshot?.daemon.stale != false {
            try await Task.sleep(for: .milliseconds(20))
        }
        XCTAssertGreaterThan(StaleConnectionProtocol.state.staleReads, 0)
        XCTAssertTrue(StaleConnectionProtocol.state.connected)
        XCTAssertEqual(model.snapshot?.daemon.stale, false)
        XCTAssertNil(model.connectionError)
    }

    @MainActor
    func testAComputerRemovedMidRefreshDoesNotApplyItsLateSnapshot() async throws {
        KeychainStore.removeAll()
        try AgenthailIOSTests.pair("hheld", "https://held.tailnet.ts.net", token: "fixture")
        HeldSnapshotProtocol.state.reset(holdAt: 1)
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [HeldSnapshotProtocol.self]
        let model = AgenthailIOSModel(autoConnect: false, session: URLSession(configuration: configuration))
        defer { KeychainStore.removeAll() }
        model.connect()
        for _ in 0..<150 where !HeldSnapshotProtocol.state.holding {
            try await Task.sleep(for: .milliseconds(20))
        }
        XCTAssertTrue(HeldSnapshotProtocol.state.holding)
        model.forgetHost("hheld")
        HeldSnapshotProtocol.state.release()
        try await Task.sleep(for: .milliseconds(300))
        XCTAssertFalse(HeldSnapshotProtocol.state.connected)
        XCTAssertNil(model.snapshot)
        XCTAssertTrue(model.hosts.isEmpty)
    }

    @MainActor
    func testAComputerRemovedDuringItsStreamRefreshStartsNoCatalogStream() async throws {
        KeychainStore.removeAll()
        try AgenthailIOSTests.pair("hheld", "https://held.tailnet.ts.net", token: "fixture")
        HeldSnapshotProtocol.state.reset(holdAt: 2)
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [HeldSnapshotProtocol.self]
        let model = AgenthailIOSModel(autoConnect: false, session: URLSession(configuration: configuration))
        defer { KeychainStore.removeAll() }
        model.connect()
        for _ in 0..<150 where !HeldSnapshotProtocol.state.holding {
            try await Task.sleep(for: .milliseconds(20))
        }
        XCTAssertTrue(HeldSnapshotProtocol.state.connected)
        XCTAssertTrue(HeldSnapshotProtocol.state.holding)
        model.forgetHost("hheld")
        HeldSnapshotProtocol.state.release()
        try await Task.sleep(for: .milliseconds(300))
        XCTAssertEqual(HeldSnapshotProtocol.state.catalogRequests, 0)
        XCTAssertNil(model.snapshot)
    }
    @MainActor
    func testASessionShowsOnlyTheStateOfTheComputerServingIt() async throws {
        KeychainStore.removeAll()
        try AgenthailIOSTests.pair("hup", "https://up.tailnet.ts.net")
        try AgenthailIOSTests.pair("hdown", "https://down.tailnet.ts.net")
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [SplitHostsProtocol.self]
        let model = AgenthailIOSModel(autoConnect: false, session: URLSession(configuration: configuration))
        defer { KeychainStore.removeAll() }
        model.connect()
        for _ in 0..<150 where model.connectionError == nil || model.snapshot == nil {
            try await Task.sleep(for: .milliseconds(20))
        }
        for _ in 0..<150 where model.connectionState(for: "hup::demo").reconnecting {
            try await Task.sleep(for: .milliseconds(20))
        }
        XCTAssertNotNil(model.connectionError)
        XCTAssertNil(model.connectionState(for: "hup::demo").error)
        XCTAssertFalse(model.connectionState(for: "hup::demo").reconnecting)
        XCTAssertNotNil(model.connectionState(for: "hdown::other").error)
    }
    @MainActor
    func testAnUnreachableMirroredSessionShowsOnlyItsMirrorsState() async throws {
        KeychainStore.removeAll()
        MirrorDropProtocol.state.reset()
        try AgenthailIOSTests.pair("hmirror", "https://mirror.tailnet.ts.net")
        try AgenthailIOSTests.pair("hdown", "https://down.tailnet.ts.net")
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [MirrorDropProtocol.self]
        let model = AgenthailIOSModel(autoConnect: false, session: URLSession(configuration: configuration))
        defer { KeychainStore.removeAll() }
        model.connect()
        for _ in 0..<150 where model.snapshot == nil || !MirrorDropProtocol.state.streaming {
            try await Task.sleep(for: .milliseconds(20))
        }
        XCTAssertNil(model.connectionState(for: "hbuild::s9").error)
        MirrorDropProtocol.state.dropMirror()
        for _ in 0..<150 where model.connectionState(for: "hbuild::s9").error == nil {
            try await Task.sleep(for: .milliseconds(20))
        }
        let mirror = model.hosts.first { $0.id == "hmirror" }
        XCTAssertEqual(mirror?.reachable, false)
        XCTAssertNotNil(mirror?.error)
        XCTAssertEqual(model.connectionState(for: "hbuild::s9").error, mirror?.error)
        XCTAssertNotEqual(model.connectionState(for: "hbuild::s9").error, model.connectionError)
    }

    @MainActor
    func testReconnectingAComputerDropsItsOldSessionWork() async throws {
        KeychainStore.removeAll()
        ServedSessionProtocol.state.reset(holdDetailOn: "old")
        defer { ServedSessionProtocol.state.release(); KeychainStore.removeAll() }
        try AgenthailIOSTests.pair("hsrv", "https://old.tailnet.ts.net", name: "srv")
        let model = AgenthailIOSModel(autoConnect: false, session: ServedSessionProtocol.session)
        model.connect()
        for _ in 0..<150 where model.snapshot == nil { try await Task.sleep(for: .milliseconds(20)) }
        let load = Task { await model.loadSession("hsrv::demo") }
        for _ in 0..<150 where !ServedSessionProtocol.state.holding { try await Task.sleep(for: .milliseconds(20)) }
        XCTAssertTrue(ServedSessionProtocol.state.holding)
        model.handlePairingURL(AgenthailIOSTests.pairingLink("new", host: "hsrv", name: "srv"))
        XCTAssertEqual(model.pairingConfirmationTitle, "Reconnect srv?")
        model.confirmPairing()
        for _ in 0..<150 where model.completedPairings == 0 { try await Task.sleep(for: .milliseconds(20)) }
        XCTAssertEqual(model.connection("hsrv")?.credential.endpoint.host, "new.tailnet.ts.net")
        model.connect()
        for _ in 0..<150 where model.selectedDetail?.session.status != "new" { try await Task.sleep(for: .milliseconds(20)) }
        ServedSessionProtocol.state.release()
        _ = await load.value
        try await Task.sleep(for: .milliseconds(500))
        XCTAssertEqual(model.selectedSessionID, "hsrv::demo")
        XCTAssertEqual(model.selectedDetail?.session.status, "new")
        XCTAssertEqual(ServedSessionProtocol.state.sessionRequests("old"), ["/api/v1/session"])
        XCTAssertTrue(ServedSessionProtocol.state.sessionRequests("new").contains("/api/v1/session-stream"))
        model.forgetHost("hsrv")
    }

    @MainActor
    func testRemovingTheMirrorServingASessionStopsItsStream() async throws {
        KeychainStore.removeAll()
        ServedSessionProtocol.state.reset(holdDetailOn: nil)
        defer { KeychainStore.removeAll() }
        try AgenthailIOSTests.pair("hmirror", "https://mirror.tailnet.ts.net")
        let model = AgenthailIOSModel(autoConnect: false, session: ServedSessionProtocol.session)
        model.connect()
        for _ in 0..<150 where model.snapshot == nil { try await Task.sleep(for: .milliseconds(20)) }
        await model.loadSession("hbuild::s9")
        for _ in 0..<150 where !ServedSessionProtocol.state.streaming("mirror") { try await Task.sleep(for: .milliseconds(20)) }
        XCTAssertEqual(model.selectedDetail?.session.status, "mirror")
        let before = model.selectedDetail?.timeline?.items
        model.forgetHost("hmirror")
        ServedSessionProtocol.state.emit("mirror", #"{"stream":"session","sessionId":"s9","seq":9,"type":"item","data":{"itemId":"late","version":1,"kind":"text","op":"append","role":"assistant","ts":"2026-10-03T12:00:00Z","body":"from a removed mirror","truncated":false}}"#)
        try await Task.sleep(for: .milliseconds(1500))
        XCTAssertEqual(model.selectedSessionID, "hbuild::s9")
        XCTAssertEqual(model.selectedDetail?.timeline?.items, before)
        XCTAssertEqual(ServedSessionProtocol.state.sessionRequests("mirror").filter { $0 == "/api/v1/session-stream" }.count, 1)
    }
}

private final class MirrorDropProtocol: URLProtocol, @unchecked Sendable {
    final class State: @unchecked Sendable {
        private let lock = NSLock()
        private var stream: MirrorDropProtocol?
        private var dropped = false
        var streaming: Bool { lock.withLock { stream != nil } }
        var offline: Bool { lock.withLock { dropped } }
        func reset() { lock.withLock { stream = nil; dropped = false } }
        func hold(_ request: MirrorDropProtocol) { lock.withLock { stream = request } }
        func dropMirror() {
            let held = lock.withLock { dropped = true; let held = stream; stream = nil; return held }
            held?.finish()
        }
    }
    static let state = State()
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        guard request.url?.host == "mirror.tailnet.ts.net", !Self.state.offline else {
            respond(503, #"{"error":"offline"}"#, finish: true)
            return
        }
        switch request.url!.path {
        case "/api/v1/version": respond(200, #"{"protocol":1,"minimumProtocol":1,"maximumProtocol":1}"#, finish: true)
        case "/api/v1/snapshot":
            var value = try! JSONSerialization.jsonObject(with: Data(SessionPreview.snapshotJSON.utf8)) as! [String: Any]
            var sessions = value["sessions"] as! [[String: Any]]
            var mirrored = sessions[0]
            mirrored["id"] = "s9"
            mirrored["host"] = ["id": "hbuild", "name": "build", "state": "connected"]
            sessions.append(mirrored)
            value["sessions"] = sessions
            respond(200, String(data: try! JSONSerialization.data(withJSONObject: value), encoding: .utf8)!, finish: true)
        case "/api/v1/events":
            Self.state.hold(self)
            respond(200, ": connected\n\n", finish: false)
        case "/api/v1/catalog-events": respond(200, ": connected\n\n", finish: false)
        default: respond(200, "{}", finish: true)
        }
    }
    override func stopLoading() {}
    fileprivate func finish() { client?.urlProtocolDidFinishLoading(self) }
    private func respond(_ status: Int, _ body: String, finish: Bool) {
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        if finish { client?.urlProtocolDidFinishLoading(self) }
    }
}

private final class SplitHostsProtocol: URLProtocol, @unchecked Sendable {
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        guard request.url?.host == "up.tailnet.ts.net" else {
            respond(503, #"{"error":"offline"}"#, finish: true)
            return
        }
        switch request.url!.path {
        case "/api/v1/version": respond(200, #"{"protocol":1,"minimumProtocol":1,"maximumProtocol":1}"#, finish: true)
        case "/api/v1/snapshot": respond(200, SessionPreview.snapshotJSON, finish: true)
        case "/api/v1/events", "/api/v1/catalog-events": respond(200, ": connected\n\n", finish: false)
        default: respond(200, "{}", finish: true)
        }
    }
    override func stopLoading() {}
    private func respond(_ status: Int, _ body: String, finish: Bool) {
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        if finish { client?.urlProtocolDidFinishLoading(self) }
    }
}

private final class HeldSnapshotProtocol: URLProtocol, @unchecked Sendable {
    final class State: @unchecked Sendable {
        private let lock = NSLock()
        private var held: HeldSnapshotProtocol?
        private var stream = false
        private var holdAt = 1
        private var snapshots = 0
        private var catalogs = 0
        var holding: Bool { lock.withLock { held != nil } }
        var connected: Bool { lock.withLock { stream } }
        var catalogRequests: Int { lock.withLock { catalogs } }
        func reset(holdAt: Int) { lock.withLock { held = nil; stream = false; self.holdAt = holdAt; snapshots = 0; catalogs = 0 } }
        func snapshot(_ request: HeldSnapshotProtocol) -> Bool {
            lock.withLock {
                snapshots += 1
                if snapshots == holdAt { held = request }
                return snapshots == holdAt
            }
        }
        func connect() { lock.withLock { stream = true } }
        func catalog() { lock.withLock { catalogs += 1 } }
        func release() {
            guard let request = lock.withLock({ held }) else { return }
            request.respond(SessionPreview.snapshotJSON)
        }
    }
    static let state = State()
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        switch request.url!.path {
        case "/api/v1/snapshot":
            if !Self.state.snapshot(self) {
                var value = try! JSONSerialization.jsonObject(with: Data(SessionPreview.snapshotJSON.utf8)) as! [String: Any]
                value["daemon"] = ["running": true, "stale": true]
                respond(String(data: try! JSONSerialization.data(withJSONObject: value), encoding: .utf8)!)
            }
        case "/api/v1/catalog-events":
            Self.state.catalog()
            respond(": connected\n\n")
        case "/api/v1/events":
            Self.state.connect()
            respond(": connected\n\n")
        case "/api/v1/version": respond(#"{"protocol":1,"minimumProtocol":1,"maximumProtocol":1}"#)
        default: respond("{}")
        }
    }
    override func stopLoading() {}
    fileprivate func respond(_ body: String) {
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
}

private final class StaleConnectionProtocol: URLProtocol, @unchecked Sendable {
    final class State: @unchecked Sendable {
        private let lock = NSLock()
        private var stream = false
        private var reads = 0
        var connected: Bool { lock.withLock { stream } }
        var staleReads: Int { lock.withLock { reads } }
        func reset() { lock.withLock { stream = false; reads = 0 } }
        func connect() { lock.withLock { stream = true } }
        func snapshot() -> String {
            lock.withLock {
                if !stream { reads += 1 }
                var value = try! JSONSerialization.jsonObject(with: Data(SessionPreview.snapshotJSON.utf8)) as! [String: Any]
                value["daemon"] = ["running": true, "stale": !stream, "refreshError": "Discovery unavailable"]
                return String(data: try! JSONSerialization.data(withJSONObject: value), encoding: .utf8)!
            }
        }
    }
    static let state = State()
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        let path = request.url!.path
        let body: String
        if path == "/api/v1/events" {
            Self.state.connect()
            let response = HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "text/event-stream"])!
            client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: Data(": connected\n\n".utf8))
            return
        } else if path == "/api/v1/version" {
            body = #"{"protocol":1,"minimumProtocol":1,"maximumProtocol":1}"#
        } else if path == "/api/v1/snapshot" { body = Self.state.snapshot() }
        else { body = "{}" }
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

private final class ServedSessionProtocol: URLProtocol, @unchecked Sendable {
    final class State: @unchecked Sendable {
        private let condition = NSCondition()
        private var holdOn: String?
        private var waiting = false
        private var log: [(host: String, path: String)] = []
        private var streams: [String: ServedSessionProtocol] = [:]
        var holding: Bool { condition.withLock { waiting } }
        func reset(holdDetailOn host: String?) {
            condition.withLock { holdOn = host; waiting = false; log = []; streams = [:]; condition.broadcast() }
        }
        func release() { condition.withLock { holdOn = nil; condition.broadcast() } }
        func record(_ host: String, _ path: String) { condition.withLock { log.append((host, path)) } }
        func sessionRequests(_ host: String) -> [String] {
            condition.withLock { log.filter { $0.host == host && $0.path.hasPrefix("/api/v1/session") }.map(\.path) }
        }
        func streaming(_ host: String) -> Bool { condition.withLock { streams[host] != nil } }
        func open(_ host: String, _ request: ServedSessionProtocol) { condition.withLock { streams[host] = request } }
        func emit(_ host: String, _ json: String) {
            let stream = condition.withLock { streams[host] }
            stream?.client?.urlProtocol(stream!, didLoad: Data("id: 9\ndata: \(json)\n\n".utf8))
        }
        func waitIfHeld(_ host: String) {
            condition.lock(); defer { condition.unlock() }
            guard holdOn == host else { return }
            waiting = true
            while holdOn == host { condition.wait() }
            waiting = false
        }
    }
    static let state = State()
    static var session: URLSession {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [ServedSessionProtocol.self]
        return URLSession(configuration: configuration)
    }
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        let host = request.url?.host?.split(separator: ".").first.map(String.init) ?? ""
        let path = request.url!.path
        Self.state.record(host, path)
        switch path {
        case "/api/v1/version": respond(#"{"protocol":1,"minimumProtocol":1,"maximumProtocol":1}"#)
        case "/api/v1/pair":
            respond(#"{"device":{"id":"d","name":"Phone","scopes":["read","control"],"createdAt":"now","pushEnabled":false},"token":"new-token","protocol":1,"host":{"id":"hsrv","name":"srv"}}"#)
        case "/api/v1/snapshot":
            var value = try! JSONSerialization.jsonObject(with: Data(SessionPreview.snapshotJSON.utf8)) as! [String: Any]
            if host == "mirror" {
                var sessions = value["sessions"] as! [[String: Any]]
                var mirrored = sessions[0]
                mirrored["id"] = "s9"
                mirrored["host"] = ["id": "hbuild", "name": "build", "state": "connected"]
                value["sessions"] = sessions + [mirrored]
            }
            respond(String(decoding: try! JSONSerialization.data(withJSONObject: value), as: UTF8.self))
        case "/api/v1/session":
            DispatchQueue.global().async { [self] in
                Self.state.waitIfHeld(host)
                var object = try! JSONSerialization.jsonObject(with: Data(SessionPreview.detailJSON.utf8)) as! [String: Any]
                var session = object["session"] as! [String: Any]
                session["status"] = host
                object["session"] = session
                respond(String(decoding: try! JSONSerialization.data(withJSONObject: object), as: UTF8.self))
            }
        case "/api/v1/session-stream":
            Self.state.open(host, self)
            open()
        case "/api/v1/events", "/api/v1/catalog-events": open()
        default: respond("{}")
        }
    }
    override func stopLoading() {}
    private func open() {
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "text/event-stream"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(": connected\n\n".utf8))
    }
    private func respond(_ body: String) {
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
}
