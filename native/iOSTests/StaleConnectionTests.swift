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
