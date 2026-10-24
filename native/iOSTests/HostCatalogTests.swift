import XCTest
@testable import Agenthail

final class HostCatalogTests: XCTestCase {
    func testAComputersOwnSessionsAreKeyedByItAndRoutedToIt() {
        let catalog = HostCatalog(hosts: [host("hlaptop", "laptop", reachable: true, sessions: [local("s1")])])
        XCTAssertEqual(catalog.sessions.map(\.id), ["hlaptop::s1"])
        XCTAssertEqual(catalog.sessions.first?.host, SessionHost(id: "hlaptop", name: "laptop", state: "connected"))
        XCTAssertEqual(catalog.route(for: "hlaptop::s1"), SessionRoute(hostID: "hlaptop", sessionID: "s1"))
    }

    func testTheOwnersRowWinsOverAMirrorWhileTheOwnerIsReachable() {
        let catalog = HostCatalog(hosts: [
            host("hlaptop", "laptop", reachable: true, sessions: [mirrored("hserver", "server", "s1", name: "mirror copy")]),
            host("hserver", "server", reachable: true, sessions: [local("s1", name: "owner copy")]),
        ])
        XCTAssertEqual(catalog.sessions.map(\.id), ["hserver::s1"])
        XCTAssertEqual(catalog.sessions.first?.name, "owner copy")
        XCTAssertEqual(catalog.routes(for: "hserver::s1"), [
            SessionRoute(hostID: "hserver", sessionID: "s1"),
            SessionRoute(hostID: "hlaptop", sessionID: "hserver::s1"),
        ])
    }

    func testAnOwnerThePhoneCannotReachIsWorkedThroughAComputerThatMirrorsIt() {
        let catalog = HostCatalog(hosts: [
            host("hlaptop", "laptop", reachable: true, sessions: [mirrored("hserver", "server", "s1", name: "mirror copy")]),
            host("hserver", "server", reachable: false, sessions: [local("s1", name: "owner copy")]),
        ])
        XCTAssertEqual(catalog.sessions.map(\.id), ["hserver::s1"])
        XCTAssertEqual(catalog.sessions.first?.name, "mirror copy")
        XCTAssertEqual(catalog.sessions.first?.unavailableReason, nil)
        XCTAssertEqual(catalog.route(for: "hserver::s1"), SessionRoute(hostID: "hlaptop", sessionID: "hserver::s1"))
    }

    func testASessionIsUnreachableOnlyWhenNoComputerServingItIs() {
        let catalog = HostCatalog(hosts: [
            host("hlaptop", "laptop", reachable: true, sessions: [local("mine")]),
            host("hserver", "server", reachable: false, sessions: [local("s1")]),
        ])
        let down = catalog.sessions.first { $0.id == "hserver::s1" }
        XCTAssertEqual(down?.unavailableReason, "server can't be reached")
        XCTAssertNil(catalog.route(for: "hserver::s1"))
        let up = catalog.sessions.first { $0.id == "hlaptop::mine" }
        XCTAssertNil(up?.unavailableReason)
        XCTAssertNotNil(catalog.route(for: "hlaptop::mine"))
    }

    func testAMirroredSessionOfAComputerThePhoneIsNotPairedWithKeepsItsQualifiedID() {
        let catalog = HostCatalog(hosts: [host("hlaptop", "laptop", reachable: true, sessions: [mirrored("hbuild", "build", "s9")])])
        XCTAssertEqual(catalog.sessions.map(\.id), ["hbuild::s9"])
        XCTAssertEqual(catalog.sessions.first?.host?.name, "build")
        XCTAssertEqual(catalog.route(for: "hbuild::s9"), SessionRoute(hostID: "hlaptop", sessionID: "hbuild::s9"))
    }

    func testAnUnreachableMirroredSessionKeepsTheComputerThatListedIt() {
        let catalog = HostCatalog(hosts: [
            host("hlaptop", "laptop", reachable: false, sessions: [mirrored("hbuild", "build", "s9")]),
            host("hserver", "server", reachable: false, sessions: [local("s1")]),
        ])
        XCTAssertNil(catalog.route(for: "hbuild::s9"))
        XCTAssertEqual(catalog.server(of: "hbuild::s9"), "hlaptop")
        XCTAssertEqual(catalog.server(of: "hserver::s1"), "hserver")
    }

    func testSubagentsAndProjectsOfALocalRowAreQualifiedWithItsComputer() {
        var child = local("child")
        child.subagent = SubagentIdentity(parentId: "parent", rootId: "parent", depth: 1)
        child.project = ProjectIdentity(id: "p1", displayName: "widget", rootPath: "/w", kind: "git")
        let catalog = HostCatalog(hosts: [host("hlaptop", "laptop", reachable: true, sessions: [child])])
        XCTAssertEqual(catalog.sessions.first?.subagent?.parentId, "hlaptop::parent")
        XCTAssertEqual(catalog.sessions.first?.subagent?.rootId, "hlaptop::parent")
        XCTAssertEqual(catalog.sessions.first?.project?.id, "hlaptop::p1")
    }

    func testARepositoryProjectKeepsOneIDOnEveryComputer() {
        var onLaptop = local("s1")
        onLaptop.project = ProjectIdentity(id: "repo:github.com/acme/widget", displayName: "widget", rootPath: "/w", kind: "git")
        var onServer = local("s2")
        onServer.project = ProjectIdentity(id: "repo:github.com/acme/widget", displayName: "widget", rootPath: "/srv/w", kind: "git")
        let catalog = HostCatalog(hosts: [
            host("hlaptop", "laptop", reachable: true, sessions: [onLaptop]),
            host("hserver", "server", reachable: true, sessions: [onServer]),
        ])
        XCTAssertEqual(Set(catalog.sessions.compactMap(\.project?.id)), ["repo:github.com/acme/widget"])
    }

    func testTheSamePathOnTwoComputersIsTwoCheckoutsOfOneProject() {
        func row(_ id: String) -> SessionState {
            var row = local(id)
            row.project = ProjectIdentity(id: "repo:github.com/acme/widget", displayName: "widget", rootPath: "/srv/widget", kind: "git")
            row.checkout = CheckoutIdentity(id: "checkout-1", path: "/srv/widget", branch: "main", detachedHead: nil, isMain: true, dirty: false)
            return row
        }
        let catalog = HostCatalog(hosts: [
            host("hlaptop", "laptop.example.ts.net", reachable: true, sessions: [row("s1")]),
            host("hserver", "server.example.ts.net", reachable: true, sessions: [row("s2")]),
        ])
        XCTAssertEqual(Set(catalog.sessions.compactMap(\.checkout?.id)), ["hlaptop::checkout-1", "hserver::checkout-1"])
        let projects = SessionTree.build(catalog.sessions, filter: .all, attention: [:]).projects
        XCTAssertEqual(projects.map(\.id), ["repo:github.com/acme/widget"])
        XCTAssertEqual(projects.first?.checkouts.count, 2)
        XCTAssertEqual(Set(projects.first?.hosts ?? []), ["laptop", "server"])
    }

    func testARepositoryIDNamesNoComputer() {
        XCTAssertEqual(HostCatalog.qualify("hlaptop", "repo:github.com/acme/widget"), "repo:github.com/acme/widget")
        XCTAssertNil(HostCatalog.owner(of: "repo:github.com/acme/widget"))
        XCTAssertEqual(HostCatalog.qualify("hlaptop", "s1"), "hlaptop::s1")
        XCTAssertEqual(HostCatalog.owner(of: "hserver::folder:notes"), "hserver")
        XCTAssertEqual(HostCatalog.qualify("", "s1"), "s1")
        XCTAssertNil(HostCatalog.owner(of: "::s1"))
        XCTAssertNil(HostCatalog.owner(of: "s1"))
    }

    func testDeliveryProblemsAndAttentionNameTheirComputer() {
        var snapshot = Self.snapshot([local("s1")])
        snapshot.deliveryProblems = [DeliveryProblem(deliveryId: 7, sessionId: "s1", sourceSessionId: "s2", attentionSessionId: "s2", message: "m", reason: "r", status: nil, at: "now")]
        let other = Self.snapshot([local("s1")], problems: [DeliveryProblem(deliveryId: 7, sessionId: "s1", sourceSessionId: nil, attentionSessionId: nil, message: "m", reason: "r", status: nil, at: "now")])
        let catalog = HostCatalog(hosts: [
            PairedHostView(id: "ha", name: "a", reachable: true, snapshot: snapshot),
            PairedHostView(id: "hb", name: "b", reachable: true, snapshot: other),
        ])
        XCTAssertEqual(catalog.deliveryProblems.map(\.sessionId), ["ha::s1", "hb::s1"])
        XCTAssertEqual(catalog.deliveryProblems.first?.sourceSessionId, "ha::s2")
        XCTAssertEqual(Set(catalog.deliveryProblems.map(\.id)).count, 2, "the same delivery id on two computers is two problems")
    }

    func testAComputerIsReachedDirectlyOrThroughAPairedComputerThatMirrorsIt() {
        var laptop = Self.snapshot([])
        laptop.daemonHosts([HostHealth(id: "hserver", name: "server", state: "connected"), HostHealth(id: "hbuild", name: "build", state: "unreachable")])
        let catalog = HostCatalog(hosts: [
            PairedHostView(id: "hlaptop", name: "laptop", reachable: true, snapshot: laptop),
            PairedHostView(id: "hserver", name: "server", reachable: false, snapshot: Self.snapshot([])),
        ])
        XCTAssertEqual(catalog.target(forHost: "hlaptop"), HostTarget(hostID: "hlaptop", remote: nil))
        XCTAssertEqual(catalog.target(forHost: "hserver"), HostTarget(hostID: "hlaptop", remote: "hserver"))
        XCTAssertNil(catalog.target(forHost: "hbuild"))
        XCTAssertEqual(catalog.hosts.map(\.id), ["hlaptop", "hserver", "hbuild"])
        XCTAssertEqual(catalog.hosts.first { $0.id == "hbuild" }?.state, "unreachable")
    }

    func testASessionNoComputerListsIsRoutedToItsOwnerByItsID() {
        var laptop = Self.snapshot([])
        laptop.daemonHosts([HostHealth(id: "hserver", name: "server", state: "connected")])
        let direct = HostCatalog(hosts: [PairedHostView(id: "hlaptop", name: "laptop", reachable: true, snapshot: laptop)])
        XCTAssertEqual(direct.route(for: "hlaptop::new"), SessionRoute(hostID: "hlaptop", sessionID: "new"))
        XCTAssertEqual(direct.route(for: "hserver::new"), SessionRoute(hostID: "hlaptop", sessionID: "hserver::new"))
        XCTAssertNil(direct.route(for: "hbuild::new"))
        XCTAssertNil(direct.route(for: "new"))
    }

    func testAQuietWorkingSessionFromTheWireSaysHowLongAndWhatItRuns() throws {
        let wire = #"{"id":"s1","surface":"claude","name":"builder","status":"busy","queueCount":0,"open":true,"current":true,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true},"host":{"id":"self","name":"self","state":"local"},"activity":{"busySince":"2026-10-23T10:00:00Z","lastProgressAt":"2026-10-23T10:02:00Z","tool":{"name":"Bash","startedAt":"2026-10-23T10:02:00Z"}}}"#
        let row = try JSONDecoder().decode(SessionState.self, from: Data(wire.utf8))
        let catalog = HostCatalog(hosts: [host("hlaptop", "laptop", reachable: true, sessions: [row])])
        let session = try XCTUnwrap(catalog.sessions.first)
        let now = try XCTUnwrap(SessionTree.parseTimestamp("2026-10-23T10:14:30Z"))
        let since = try XCTUnwrap(SessionTree.parseTimestamp("2026-10-23T10:02:00Z")).formatted(date: .omitted, time: .shortened)
        XCTAssertEqual(session.workingLabel(now: now), "Working · no activity for 12 min · running Bash since \(since)")
        XCTAssertEqual(session.workingLabel(now: now.addingTimeInterval(-5 * 60)), "Working")

        let orphan = try JSONDecoder().decode(RawSession.self, from: Data(#"{"id":"s2","surface":"claude","name":"held","status":"busy","lastActive":"2026-10-23T05:07:57Z","activity":{"lastProgressAt":"2026-10-23T05:07:57Z","noProcess":true}}"#.utf8))
        XCTAssertEqual(orphan.workingLabel(now: now), "Working · no activity for 5 hr 6 min · no Claude process")

        var live = SessionLiveState(row, lastActive: "2026-10-23T10:00:00Z")
        XCTAssertEqual(live.activity, row.activity)
        live.activity = nil
        XCTAssertNil(live.activity)
    }

    private func host(_ id: String, _ name: String, reachable: Bool, sessions: [SessionState]) -> PairedHostView {
        PairedHostView(id: id, name: name, reachable: reachable, snapshot: Self.snapshot(sessions))
    }

    private func local(_ id: String, name: String? = nil) -> SessionState {
        var row = Self.row(id, name: name ?? id)
        row.host = SessionHost(id: "self", name: "self", state: "local")
        return row
    }

    private func mirrored(_ owner: String, _ ownerName: String, _ id: String, name: String? = nil) -> SessionState {
        var row = Self.row(owner + "::" + id, name: name ?? id)
        row.host = SessionHost(id: owner, name: ownerName, state: "connected")
        return row
    }

    private static func row(_ id: String, name: String) -> SessionState {
        SessionState(id: id, surface: "claude", name: name, alias: nil, status: "idle", lastActive: nil, queueCount: 0,
                     open: true, current: true, currentReason: nil, capabilities: Capabilities(send: true, steer: true), readOnly: false, readOnlyReason: nil)
    }

    static func snapshot(_ sessions: [SessionState], problems: [DeliveryProblem]? = nil) -> DashboardSnapshot {
        DashboardSnapshot(updatedAt: "2026-10-22T12:00:00Z", eventCursor: 1, hostEpoch: "e", catalogSeq: 1, daemon: DaemonState(running: true, pid: 1, stale: false, refreshError: nil), surfaces: [], sessions: sessions, totalSessions: sessions.count, queue: [], channels: [], relays: [], history: [], attention: [], deliveryProblems: problems, codexRecentHours: 8, busyDelivery: "queue")
    }
}

final class RepositoryArchiveTests: XCTestCase {
    @MainActor
    func testArchivingARepositoryReachesEveryComputerEvenWhenOneRefuses() async throws {
        KeychainStore.removeAll()
        RepositoryArchiveProtocol.state.reset(refusing: "laptop.tailnet.ts.net")
        try AgenthailIOSTests.pair("hlaptop", "https://laptop.tailnet.ts.net", name: "laptop")
        try AgenthailIOSTests.pair("hserver", "https://server.tailnet.ts.net", name: "server")
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [RepositoryArchiveProtocol.self]
        let model = AgenthailIOSModel(autoConnect: false, session: URLSession(configuration: configuration))
        defer { KeychainStore.removeAll() }
        await model.setProjectArchived("repo:github.com/acme/widget", true)
        XCTAssertEqual(model.operationError, "Connect to your computer first.")
        XCTAssertTrue(RepositoryArchiveProtocol.state.actions.isEmpty)

        for (hostID, session) in [("hlaptop", "s1"), ("hserver", "s2")] {
            model.setHostSnapshot(try JSONDecoder().decode(DashboardSnapshot.self, from: Data(#"{"updatedAt":"now","daemon":{"running":true},"surfaces":[],"sessions":[{"id":"\#(session)","surface":"codex","name":"task","status":"idle","queueCount":0,"open":true,"current":false,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true},"project":{"id":"repo:github.com/acme/widget","displayName":"widget","rootPath":"/w","kind":"git"}}],"totalSessions":1,"queue":[],"channels":[],"relays":[],"history":[],"attention":[],"codexRecentHours":24}"#.utf8)), host: hostID)
        }
        await model.setProjectArchived("repo:github.com/acme/widget", true)
        let actions = RepositoryArchiveProtocol.state.actions
        XCTAssertEqual(Set(actions.map(\.host)), ["laptop.tailnet.ts.net", "server.tailnet.ts.net"])
        XCTAssertEqual(Set(actions.map(\.projectID)), ["repo:github.com/acme/widget"])
        XCTAssertEqual(model.operationError, "laptop: busy")
    }
}

private final class RepositoryArchiveProtocol: URLProtocol, @unchecked Sendable {
    final class State: @unchecked Sendable {
        private let lock = NSLock()
        private var refusing = ""
        private var records: [(host: String, projectID: String)] = []
        var actions: [(host: String, projectID: String)] { lock.withLock { records } }
        func reset(refusing host: String) { lock.withLock { refusing = host; records = [] } }
        func act(host: String, projectID: String) -> Bool { lock.withLock { records.append((host, projectID)); return host != refusing } }
    }
    static let state = State()
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        var status = 503
        var text = #"{"error":{"message":"unavailable"}}"#
        if request.url?.path == "/api/v1/actions" {
            var data = request.httpBody ?? Data()
            if let stream = request.httpBodyStream { stream.open(); defer { stream.close() }; var buffer = [UInt8](repeating: 0, count: 4096); while stream.hasBytesAvailable { let n = stream.read(&buffer, maxLength: buffer.count); if n <= 0 { break }; data.append(contentsOf: buffer.prefix(n)) } }
            let body = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:]
            if Self.state.act(host: request.url?.host ?? "", projectID: body["projectId"] as? String ?? "") { (status, text) = (200, #"{"ok":true}"#) } else { (status, text) = (409, #"{"error":{"message":"busy"}}"#) }
        }
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(text.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

private extension DashboardSnapshot {
    mutating func daemonHosts(_ hosts: [HostHealth]) {
        self = DashboardSnapshot(updatedAt: updatedAt, eventCursor: eventCursor, hostEpoch: hostEpoch, catalogSeq: catalogSeq, daemon: DaemonState(running: true, pid: 1, stale: false, refreshError: nil, hosts: hosts), surfaces: surfaces, sessions: sessions, totalSessions: totalSessions, queue: queue, channels: channels, relays: relays, history: history, attention: attention, deliveryProblems: deliveryProblems, codexRecentHours: codexRecentHours, busyDelivery: busyDelivery)
    }
}
