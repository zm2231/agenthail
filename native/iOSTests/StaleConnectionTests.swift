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
        XCTAssertEqual(ServedSessionProtocol.state.sessionRequests("old").sorted(), ["/api/v1/session", "/api/v1/session-commands"])
        XCTAssertTrue(ServedSessionProtocol.state.sessionRequests("new").contains("/api/v1/session-stream"))
        model.forgetHost("hsrv")
    }

    @MainActor
    func testReconnectingAComputerKeepsOnlyTheCommandsItsReplacementLists() async throws {
        KeychainStore.removeAll()
        ServedSessionProtocol.state.reset(holdDetailOn: nil)
        ServedSessionProtocol.state.hold("old", ["/api/v1/session-commands"])
        defer { ServedSessionProtocol.state.release(); KeychainStore.removeAll() }
        try AgenthailIOSTests.pair("hsrv", "https://old.tailnet.ts.net", name: "srv")
        let model = AgenthailIOSModel(autoConnect: false, session: ServedSessionProtocol.session)
        model.connect()
        for _ in 0..<150 where model.snapshot == nil { try await Task.sleep(for: .milliseconds(20)) }
        await model.loadSession("hsrv::demo")
        for _ in 0..<150 where !ServedSessionProtocol.state.holding { try await Task.sleep(for: .milliseconds(20)) }
        XCTAssertTrue(ServedSessionProtocol.state.holding)
        model.handlePairingURL(AgenthailIOSTests.pairingLink("new", host: "hsrv", name: "srv"))
        model.confirmPairing()
        for _ in 0..<150 where model.completedPairings == 0 { try await Task.sleep(for: .milliseconds(20)) }
        model.connect()
        for _ in 0..<150 where model.store.commands["hsrv::demo"] == nil { try await Task.sleep(for: .milliseconds(20)) }
        ServedSessionProtocol.state.release()
        for _ in 0..<150 where ServedSessionProtocol.state.holding { try await Task.sleep(for: .milliseconds(20)) }
        try await Task.sleep(for: .milliseconds(300))
        XCTAssertEqual(model.store.commands["hsrv::demo"]?.commands.map(\.name), ["new"])
        XCTAssertEqual(ServedSessionProtocol.state.sessionRequests("new").filter { $0 == "/api/v1/session-commands" }.count, 1)
        model.forgetHost("hsrv")
        XCTAssertNil(model.store.commands["hsrv::demo"])
    }

    @MainActor
    func testReconnectingAComputerDropsItsOldRequestsAndOlderActivity() async throws {
        KeychainStore.removeAll()
        ServedSessionProtocol.state.reset(holdDetailOn: nil)
        defer { ServedSessionProtocol.state.release(); KeychainStore.removeAll() }
        try AgenthailIOSTests.pair("hsrv", "https://old.tailnet.ts.net", name: "srv")
        let model = AgenthailIOSModel(autoConnect: false, session: ServedSessionProtocol.session)
        model.connect()
        for _ in 0..<150 where model.snapshot == nil { try await Task.sleep(for: .milliseconds(20)) }
        await model.loadSession("hsrv::demo")
        let session = try XCTUnwrap(model.snapshot?.sessions.first { $0.id == "hsrv::demo" })
        XCTAssertTrue(session.capabilities.respond)
        XCTAssertEqual(model.selectedDetail?.session.status, "old")
        XCTAssertNotNil(model.activityCursor)
        ServedSessionProtocol.state.hold("old", ["/api/v1/requests", "/api/v1/session"])
        let requests = Task { await model.syncRequests(for: session) }
        let older = Task { await model.loadOlderActivity() }
        for _ in 0..<150 where ServedSessionProtocol.state.held < 2 { try await Task.sleep(for: .milliseconds(20)) }
        XCTAssertEqual(ServedSessionProtocol.state.held, 2)
        model.handlePairingURL(AgenthailIOSTests.pairingLink("new", host: "hsrv", name: "srv"))
        model.confirmPairing()
        for _ in 0..<150 where model.completedPairings == 0 { try await Task.sleep(for: .milliseconds(20)) }
        model.connect()
        for _ in 0..<150 where model.sessionRequests?.permissionMode != "new" { try await Task.sleep(for: .milliseconds(20)) }
        XCTAssertEqual(model.selectedDetail?.session.status, "new")
        ServedSessionProtocol.state.release()
        await requests.value
        await older.value
        try await Task.sleep(for: .milliseconds(300))
        XCTAssertEqual(model.sessionRequests?.permissionMode, "new")
        XCTAssertTrue(model.olderActivity.isEmpty)
        XCTAssertNil(model.olderActivityError)
        model.forgetHost("hsrv")
    }

    @MainActor
    func testAnOldComputersHistoryGapDoesNotStopOlderActivityOnItsReplacement() async throws {
        KeychainStore.removeAll()
        ServedSessionProtocol.state.reset(holdDetailOn: nil)
        defer { ServedSessionProtocol.state.release(); KeychainStore.removeAll() }
        try AgenthailIOSTests.pair("hsrv", "https://old.tailnet.ts.net", name: "srv")
        let model = AgenthailIOSModel(autoConnect: false, session: ServedSessionProtocol.session)
        model.connect()
        for _ in 0..<150 where model.snapshot == nil { try await Task.sleep(for: .milliseconds(20)) }
        await model.loadSession("hsrv::demo")
        let cursor = try XCTUnwrap(model.activityCursor)
        ServedSessionProtocol.state.gap("old")
        ServedSessionProtocol.state.hold("old", ["/api/v1/session"])
        let older = Task { await model.loadOlderActivity() }
        for _ in 0..<150 where !ServedSessionProtocol.state.holding { try await Task.sleep(for: .milliseconds(20)) }
        model.handlePairingURL(AgenthailIOSTests.pairingLink("new", host: "hsrv", name: "srv"))
        model.confirmPairing()
        for _ in 0..<150 where model.completedPairings == 0 { try await Task.sleep(for: .milliseconds(20)) }
        model.connect()
        for _ in 0..<150 where model.selectedDetail?.session.status != "new" || model.activityCursor == nil { try await Task.sleep(for: .milliseconds(20)) }
        XCTAssertEqual(model.activityCursor, cursor)
        ServedSessionProtocol.state.release()
        await older.value
        XCTAssertEqual(model.activityCursor, cursor)
        XCTAssertNil(model.olderActivityError)
        model.forgetHost("hsrv")
    }

    @MainActor
    func testARouteMovedToAMirrorFetchesSubagentsAndAttachmentsThroughIt() async throws {
        KeychainStore.removeAll()
        ServedSessionProtocol.state.reset(holdDetailOn: nil)
        defer { KeychainStore.removeAll() }
        try AgenthailIOSTests.pair("hown", "https://own.tailnet.ts.net")
        try AgenthailIOSTests.pair("hmirror", "https://mirror.tailnet.ts.net")
        let model = AgenthailIOSModel(autoConnect: false, session: ServedSessionProtocol.session)
        model.connect()
        for _ in 0..<150 where model.connection("hown")?.reachable != true || model.connection("hmirror")?.reachable != true {
            try await Task.sleep(for: .milliseconds(20))
        }
        await model.loadSession("hown::demo")
        XCTAssertEqual(model.selectedDetail?.session.status, "own")
        var parent = try XCTUnwrap(model.snapshot?.sessions.first { $0.id == "hown::demo" })
        parent.hiddenSubagents = SubagentRollup(count: 1, working: 0)
        await model.loadSubagents(of: parent)
        XCTAssertEqual(model.fetchedSubagents.children(of: parent.id).map(\.name), ["child"], model.fetchedSubagents.errors[parent.id] ?? "")
        XCTAssertTrue(model.fetchedSubagents.children(of: parent.id).allSatisfy { $0.id.hasSuffix("child-own") })
        let item = TimelineItem(id: "image", kind: "attachment", role: "user", title: "Image", text: "Image attachment", timestamp: nil, callId: nil, status: nil, truncated: false, truncationReason: nil, bodyRef: nil, attachment: SessionAttachment(id: "a1", mediaType: "image/png", width: nil, height: nil, bytes: nil))
        let ownBytes = await model.sessionAttachment(for: item, sessionID: parent.id)
        XCTAssertEqual(ownBytes, Data("own".utf8))
        let revision = model.fetchedSubagents.revision(parent.id)
        for _ in 0..<150 where model.store.commands[parent.id] == nil { try await Task.sleep(for: .milliseconds(20)) }
        XCTAssertEqual(model.store.commands[parent.id]?.commands.map(\.name), ["own"])

        ServedSessionProtocol.state.takeOffline("own")
        for _ in 0..<200 where model.selectedDetail?.session.status != "mirror" { try await Task.sleep(for: .milliseconds(20)) }
        XCTAssertEqual(model.selectedDetail?.session.status, "mirror")
        for _ in 0..<150 where model.store.commands[parent.id]?.commands.first?.name != "mirror" { try await Task.sleep(for: .milliseconds(20)) }
        XCTAssertEqual(model.store.commands[parent.id]?.commands.map(\.name), ["mirror"])
        XCTAssertEqual(ServedSessionProtocol.state.sessionRequests("mirror").filter { $0 == "/api/v1/session-commands" }.count, 1)
        XCTAssertNotEqual(model.fetchedSubagents.revision(parent.id), revision)
        XCTAssertTrue(model.fetchedSubagents.children(of: parent.id).isEmpty)
        let mirrorBytes = await model.sessionAttachment(for: item, sessionID: parent.id)
        XCTAssertEqual(mirrorBytes, Data("mirror".utf8))
        await model.loadSubagents(of: parent)
        XCTAssertTrue(model.fetchedSubagents.children(of: parent.id).allSatisfy { $0.id.hasSuffix("child-mirror") })
        XCTAssertFalse(model.fetchedSubagents.children(of: parent.id).isEmpty)
        model.forgetHost("hown")
        model.forgetHost("hmirror")
    }

    @MainActor
    func testSubagentsThatFailedWhileUnreachableLoadOnceTheComputerIsBack() async throws {
        KeychainStore.removeAll()
        ServedSessionProtocol.state.reset(holdDetailOn: nil)
        defer { KeychainStore.removeAll() }
        try AgenthailIOSTests.pair("hown", "https://own.tailnet.ts.net")
        let model = AgenthailIOSModel(autoConnect: false, session: ServedSessionProtocol.session)
        model.connect()
        for _ in 0..<150 where model.connection("hown")?.reachable != true { try await Task.sleep(for: .milliseconds(20)) }
        var parent = try XCTUnwrap(model.snapshot?.sessions.first { $0.id == "hown::demo" })
        parent.hiddenSubagents = SubagentRollup(count: 1, working: 0)
        await model.loadSubagents(of: parent)
        XCTAssertFalse(model.fetchedSubagents.children(of: parent.id).isEmpty)

        ServedSessionProtocol.state.takeOffline("own")
        let cached = model.fetchedSubagents.revision(parent.id)
        for _ in 0..<200 where model.fetchedSubagents.revision(parent.id) == cached { try await Task.sleep(for: .milliseconds(20)) }
        await model.loadSubagents(of: parent)
        XCTAssertNotNil(model.fetchedSubagents.errors[parent.id])

        ServedSessionProtocol.state.bringOnline("own")
        let failed = model.fetchedSubagents.revision(parent.id)
        model.reconnect(host: "hown")
        for _ in 0..<200 where model.fetchedSubagents.revision(parent.id) == failed { try await Task.sleep(for: .milliseconds(20)) }
        XCTAssertNotEqual(model.fetchedSubagents.revision(parent.id), failed)
        await model.loadSubagents(of: parent)
        XCTAssertNil(model.fetchedSubagents.errors[parent.id])
        XCTAssertTrue(model.fetchedSubagents.children(of: parent.id).allSatisfy { $0.id.hasSuffix("child-own") })
        XCTAssertFalse(model.fetchedSubagents.children(of: parent.id).isEmpty)
        model.forgetHost("hown")
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
        private var holdPaths: Set<String> = []
        private var waiting = 0
        private var log: [(host: String, path: String)] = []
        private var streams: [String: ServedSessionProtocol] = [:]
        private var opened: [String: [ServedSessionProtocol]] = [:]
        private var down: Set<String> = []
        private var gapOn: String?
        func gap(_ host: String?) { condition.withLock { gapOn = host } }
        func gapped(_ host: String) -> Bool { condition.withLock { gapOn == host } }
        var holding: Bool { condition.withLock { waiting > 0 } }
        var held: Int { condition.withLock { waiting } }
        func reset(holdDetailOn host: String?) {
            condition.withLock { holdOn = host; holdPaths = ["/api/v1/session"]; waiting = 0; log = []; streams = [:]; opened = [:]; down = []; gapOn = nil; condition.broadcast() }
        }
        func hold(_ host: String, _ paths: Set<String>) { condition.withLock { holdOn = host; holdPaths = paths } }
        func release() { condition.withLock { holdOn = nil; condition.broadcast() } }
        func record(_ host: String, _ path: String) { condition.withLock { log.append((host, path)) } }
        func sessionRequests(_ host: String) -> [String] {
            condition.withLock { log.filter { $0.host == host && $0.path.hasPrefix("/api/v1/session") }.map(\.path) }
        }
        func streaming(_ host: String) -> Bool { condition.withLock { streams[host] != nil } }
        func open(_ host: String, _ request: ServedSessionProtocol) { condition.withLock { streams[host] = request } }
        func opened(_ host: String, _ request: ServedSessionProtocol) { condition.withLock { opened[host, default: []].append(request) } }
        func offline(_ host: String) -> Bool { condition.withLock { down.contains(host) } }
        func bringOnline(_ host: String) { condition.withLock { _ = down.remove(host) } }
        func takeOffline(_ host: String) {
            let open = condition.withLock { down.insert(host); return opened.removeValue(forKey: host) ?? [] }
            for request in open { request.client?.urlProtocolDidFinishLoading(request) }
        }
        func emit(_ host: String, _ json: String) {
            let stream = condition.withLock { streams[host] }
            stream?.client?.urlProtocol(stream!, didLoad: Data("id: 9\ndata: \(json)\n\n".utf8))
        }
        func waitIfHeld(_ host: String, _ path: String) {
            condition.lock(); defer { condition.unlock() }
            guard holdOn == host, holdPaths.contains(path) else { return }
            waiting += 1
            while holdOn == host { condition.wait() }
            waiting -= 1
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
        if Self.state.offline(host) {
            client?.urlProtocol(self, didFailWithError: URLError(.cannotConnectToHost))
            return
        }
        if path == "/api/v1/snapshot", request.url?.query?.contains("parentId") == true {
            respond(#"{"updatedAt":"now","daemon":{"running":true},"surfaces":[],"sessions":[{"id":"child-\#(host)","surface":"codex","name":"child","status":"idle","queueCount":0,"open":false,"current":false,"capabilities":{"send":true,"stream":true,"reply":true,"goal":true,"compact":true,"model":true,"interrupt":true,"steer":true},"subagent":{"parentId":"demo","rootId":"demo","depth":1}}],"totalSessions":1,"queue":[],"channels":[],"relays":[],"history":[],"attention":[],"codexRecentHours":24}"#)
            return
        }
        switch path {
        case "/api/v1/version": respond(#"{"protocol":1,"minimumProtocol":1,"maximumProtocol":1}"#)
        case "/api/v1/pair":
            respond(#"{"device":{"id":"d","name":"Phone","scopes":["read","control"],"createdAt":"now","pushEnabled":false},"token":"new-token","protocol":1,"host":{"id":"hsrv","name":"srv"}}"#)
        case "/api/v1/snapshot":
            var value = try! JSONSerialization.jsonObject(with: Data(SessionPreview.snapshotJSON.utf8)) as! [String: Any]
            value["sessions"] = (value["sessions"] as! [[String: Any]]).map { session in
                var session = session
                var capabilities = session["capabilities"] as? [String: Any] ?? [:]
                capabilities["respond"] = true
                session["capabilities"] = capabilities
                return session
            }
            if host == "mirror" {
                let sessions = value["sessions"] as! [[String: Any]]
                var mirrored = sessions[0]
                mirrored["id"] = "s9"
                mirrored["host"] = ["id": "hbuild", "name": "build", "state": "connected"]
                var owned = sessions.first { $0["id"] as? String == "demo" }!
                owned["host"] = ["id": "hown", "name": "own", "state": "connected"]
                value["sessions"] = sessions + [mirrored, owned]
            }
            respond(String(decoding: try! JSONSerialization.data(withJSONObject: value), as: UTF8.self))
        case "/api/v1/session":
            let older = request.url?.query?.contains("timelineBefore") == true
            DispatchQueue.global().async { [self] in
                Self.state.waitIfHeld(host, path)
                if older, Self.state.gapped(host) {
                    respond(#"{"error":{"code":"history_gap","message":"Older history is no longer retained."}}"#, status: 409)
                    return
                }
                var object = try! JSONSerialization.jsonObject(with: Data(SessionPreview.detailJSON.utf8)) as! [String: Any]
                var session = object["session"] as! [String: Any]
                session["status"] = host
                object["session"] = session
                if older, var timeline = object["timeline"] as? [String: Any], let items = timeline["items"] as? [[String: Any]] {
                    timeline["items"] = items.map { item in var item = item; item["id"] = "older-\(host)-\(item["id"] ?? "")"; return item }
                    object["timeline"] = timeline
                }
                respond(String(decoding: try! JSONSerialization.data(withJSONObject: object), as: UTF8.self))
            }
        case "/api/v1/requests":
            DispatchQueue.global().async { [self] in
                Self.state.waitIfHeld(host, path)
                respond(#"{"permissionMode":"\#(host)","requests":[]}"#)
            }
        case "/api/v1/session-commands":
            DispatchQueue.global().async { [self] in
                Self.state.waitIfHeld(host, path)
                respond(#"{"commands":[{"name":"\#(host)","description":"","argumentHint":"","insert":"/\#(host)"}],"unavailable":""}"#)
            }
        case "/api/v1/session-stream":
            Self.state.open(host, self)
            open()
        case "/api/v1/session-attachment": respond(host)
        case "/api/v1/events", "/api/v1/catalog-events":
            Self.state.opened(host, self)
            open()
        default: respond("{}")
        }
    }
    override func stopLoading() {}
    private func open() {
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "text/event-stream"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(": connected\n\n".utf8))
    }
    private func respond(_ body: String, status: Int = 200) {
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
}
