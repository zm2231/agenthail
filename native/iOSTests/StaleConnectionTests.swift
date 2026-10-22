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
