import XCTest
@testable import Agenthail

@MainActor
final class ProjectContentsTests: XCTestCase {
    private static let projectID = "repo:github.com/demo/widget"

    private static func project(lead: String? = "lead-session") -> [String: Any] {
        var project: [String: Any] = ["id": projectID, "displayName": "widget", "checkouts": [["host": ["id": "one", "name": "one", "state": "local"], "projectId": "p-widget", "rootPath": "/work/widget"]]]
        if let lead { project["coordinators"] = [["name": "collab", "surface": "claude", "sessionId": lead]] }
        return project
    }

    private static func row(_ id: String, current: Bool = false, status: String = "idle", lastActive: String = "2026-11-01T00:00:00Z", project: String? = projectID, archived: Bool = false, parent: String? = nil) -> [String: Any] {
        var row = ScopeServer.row(id)
        row["current"] = current
        row["status"] = status
        row["lastActive"] = lastActive
        row["archived"] = archived
        row["queueCount"] = 0
        if let project { row["project"] = ["id": project, "displayName": "widget", "rootPath": "/work/widget", "kind": "git"] }
        if let parent { row["subagent"] = ["parentId": parent, "rootId": parent, "depth": 1] }
        return row
    }

    private static func sessions(_ rows: [[String: Any]]) throws -> [SessionState] {
        try JSONDecoder().decode([SessionState].self, from: JSONSerialization.data(withJSONObject: rows))
    }

    func testAProjectListsItsLeadOnceAndSplitsRunningRecentAndEarlier() throws {
        let state = try JSONDecoder().decode(ProjectState.self, from: JSONSerialization.data(withJSONObject: Self.project()))
        let listed = ListedProject(state: state, hostID: "one")
        let rows = try Self.sessions([
            Self.row("lead-session", current: true),
            Self.row("busy", current: true, status: "busy"),
            Self.row("fresh", current: true),
            Self.row("old", lastActive: "2026-10-01T00:00:00Z"),
            Self.row("older", lastActive: "2026-09-01T00:00:00Z"),
            Self.row("elsewhere", current: true, project: "repo:github.com/demo/other"),
            Self.row("child", current: true, status: "busy", parent: "busy"),
            Self.row("shelved", archived: true),
        ]).map { var row = $0; row.id = HostCatalog.qualify("one", row.id); return row }

        let contents = ProjectContents.build(listed, sessions: rows)

        XCTAssertEqual(contents.lead?.name, "collab")
        XCTAssertEqual(contents.leadSession?.id, "one::lead-session")
        XCTAssertEqual(contents.running.map(\.id), ["one::busy"])
        XCTAssertEqual(contents.recent.map(\.id), ["one::fresh"])
        XCTAssertEqual(contents.earlier.map(\.id), ["one::old", "one::older"])
    }

    func testRunningAgreesWithTheSessionListAndRecentIsNewestFirstAcrossComputers() throws {
        let state = try JSONDecoder().decode(ProjectState.self, from: JSONSerialization.data(withJSONObject: Self.project(lead: nil)))
        let listed = ListedProject(state: state, hostID: "one")
        var queued = Self.row("one::queued", current: true, lastActive: "2026-11-01T03:00:00Z")
        queued["queueCount"] = 1
        var far = Self.row("forge::far", current: true, lastActive: "2026-11-01T11:20:00.5Z")
        far["host"] = ["id": "forge", "name": "forge", "state": "connected"]
        let rows = try Self.sessions([
            Self.row("one::busy", current: true, status: "busy"),
            queued,
            Self.row("one::photos", current: true, lastActive: "2026-11-01T10:00:00Z"),
            far,
        ])

        let contents = ProjectContents.build(listed, sessions: rows)

        XCTAssertEqual(contents.running.map(\.id), ["one::busy"], "a queued idle session counted as running")
        XCTAssertEqual(contents.recent.map(\.id), ["forge::far", "one::photos", "one::queued"], "Recent is not newest first across computers")
    }

    func testALeadWithAConversationNeverClaimsItHasNotStarted() {
        XCTAssertEqual(ProjectContents.leadNote(ProjectState.Lead(name: "new", surface: "claude")), "starts with the first message sent to it")
        XCTAssertEqual(ProjectContents.leadNote(ProjectState.Lead(name: "empty", surface: "claude", sessionId: "")), "starts with the first message sent to it")
        XCTAssertEqual(ProjectContents.leadNote(ProjectState.Lead(name: "ended", surface: "claude", sessionId: "s1")), "not running · the next message picks it up")
    }

    func testAProjectWithoutALeadOffersOne() throws {
        let state = try JSONDecoder().decode(ProjectState.self, from: JSONSerialization.data(withJSONObject: Self.project(lead: nil)))
        let contents = ProjectContents.build(ListedProject(state: state, hostID: "one"), sessions: [])
        XCTAssertNil(contents.lead)
        XCTAssertNil(contents.leadSession)
    }

    func testEarlierIsReadPageByPageForTheProjectOnItsComputer() async throws {
        ScopeServer.state.reset()
        KeychainStore.removeAll()
        defer { KeychainStore.removeAll() }
        ScopeServer.state.set("one", current: [Self.row("lead-session", current: true)], scopes: ["all": [[Self.row("old")], [Self.row("older", lastActive: "2026-09-01T00:00:00Z")]]])
        ScopeServer.state.list("one", projects: [Self.project()])
        try AgenthailIOSTests.pair("one", "https://one.tailnet.ts.net")
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [ScopeServer.self]
        let model = AgenthailIOSModel(autoConnect: false, session: URLSession(configuration: configuration))
        defer { model.forgetHost("one") }
        _ = await model.refresh(fresh: true)
        let listed = try XCTUnwrap(model.projects[Self.projectID])
        XCTAssertEqual(ProjectContents.build(listed, sessions: model.snapshot?.sessions ?? []).leadSession?.id, "one::lead-session")
        XCTAssertTrue(model.hasEarlier(listed))

        await model.listEarlier(listed)
        await model.listEarlier(listed)

        XCTAssertEqual(ProjectContents.build(listed, sessions: model.snapshot?.sessions ?? []).earlier.map(\.id), ["one::old", "one::older"])
        XCTAssertFalse(model.hasEarlier(listed))
        let reads = ScopeServer.state.requests("one").filter { $0.contains("scope=all") }
        XCTAssertEqual(reads.count, 2)
        XCTAssertTrue(reads.allSatisfy { $0.contains("projectId=\(Self.projectID)") }, "Earlier was not read by the project's id: \(reads)")
        XCTAssertTrue(reads.last?.contains("cursor=1") == true)
    }

    func testEarlierReachesACheckoutMirroredByThePairedComputerAndEachDirectlyPairedOne() async throws {
        ScopeServer.state.reset()
        KeychainStore.removeAll()
        defer { KeychainStore.removeAll() }
        var project = Self.project(lead: nil)
        project["checkouts"] = [
            ["host": ["id": "hpeer", "name": "peer", "state": "connected"], "projectId": "hpeer::p-widget", "rootPath": "/srv/widget"],
            ["host": ["id": "two", "name": "two", "state": "connected"], "projectId": "two::p-widget", "rootPath": "/work/widget"],
        ]
        var mirrored = Self.row("hpeer::far")
        mirrored["host"] = ["id": "hpeer", "name": "peer", "state": "connected"]
        ScopeServer.state.set("one", current: [], scopes: ["all": [[mirrored, Self.row("unrelated", project: "repo:github.com/demo/other")]]])
        ScopeServer.state.list("one", projects: [project])
        ScopeServer.state.set("two", current: [], scopes: ["all": [[Self.row("near")]]])
        try AgenthailIOSTests.pair("one", "https://one.tailnet.ts.net")
        try AgenthailIOSTests.pair("two", "https://two.tailnet.ts.net")
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [ScopeServer.self]
        let model = AgenthailIOSModel(autoConnect: false, session: URLSession(configuration: configuration))
        defer { model.forgetHost("one"); model.forgetHost("two") }
        _ = await model.refresh(fresh: true)
        let listed = try XCTUnwrap(model.projects[Self.projectID])
        XCTAssertEqual(listed.hostID, "one")
        XCTAssertTrue(model.hasEarlier(listed))

        await model.listEarlier(listed)

        XCTAssertEqual(Set(ProjectContents.build(listed, sessions: model.snapshot?.sessions ?? []).earlier.map(\.id)), ["hpeer::far", "two::near"])
        for host in ["one", "two"] {
            XCTAssertTrue(ScopeServer.state.requests(host).contains { $0.contains("scope=all") && $0.contains("projectId=\(Self.projectID)") }, "\(host) was not asked for the project: \(ScopeServer.state.requests(host))")
        }
    }

    func testOnlyAnIdleLeadOnAReachableComputerCanBeResumed() throws {
        var offline = Self.row("offline")
        offline["host"] = ["id": "hpeer", "name": "peer", "state": "unreachable"]
        offline["unavailableReason"] = "peer can't be reached"
        let rows = try Self.sessions([Self.row("idle"), Self.row("busy", status: "busy"), offline])
        XCTAssertTrue(ProjectContents.resumable(rows[0]))
        XCTAssertFalse(ProjectContents.resumable(rows[1]))
        XCTAssertFalse(ProjectContents.resumable(rows[2]), "a lead whose computer is offline was offered Resume")
    }

    func testTheLeadOffersResumeOnlyWhenIdleAndAsksForALifecycleResumeOnlyWhenTheSessionCan() throws {
        var capable = Self.row("capable")
        var capabilities = capable["capabilities"] as? [String: Any] ?? [:]
        capabilities["lifecycle"] = ["resume": true]
        capable["capabilities"] = capabilities
        var offline = capable
        offline["id"] = "offline"
        offline["host"] = ["id": "hpeer", "name": "peer", "state": "unreachable"]
        offline["unavailableReason"] = "peer can't be reached"
        let rows = try Self.sessions([Self.row("busy", status: "busy"), capable, Self.row("plain"), offline])

        XCTAssertEqual(ProjectContents.leadActions(rows[0]), [.open, .message], "a working lead was offered Resume")
        XCTAssertEqual(ProjectContents.leadActions(rows[1]), [.resume(enabled: true, lifecycle: true), .open])
        XCTAssertEqual(ProjectContents.leadActions(rows[2]), [.resume(enabled: true, lifecycle: false), .open], "Resume without the lifecycle capability opens the lead to message it and sends no resume")
        XCTAssertEqual(ProjectContents.leadActions(rows[3]), [.resume(enabled: false, lifecycle: false), .open], "an offline lead's Resume must be disabled and send nothing")
    }

    func testAWorkingRowShowsHowLongItHasWorkedNeverItsLastActiveTime() throws {
        let now = Date()
        let stamp = ISO8601DateFormatter()
        var busy = Self.row("busy", status: "busy", lastActive: "2026-09-01T00:00:00Z")
        busy["activity"] = ["busySince": stamp.string(from: now.addingTimeInterval(-12 * 60))]
        var started = Self.row("started", status: "busy", lastActive: "2026-09-01T00:00:00Z")
        started["activity"] = ["busySince": stamp.string(from: now.addingTimeInterval(-20))]
        let idle = Self.row("idle", lastActive: stamp.string(from: now.addingTimeInterval(-5 * 3600 - 60)))
        let rows = try Self.sessions([busy, started, Self.row("unknown", status: "busy"), idle])

        XCTAssertEqual(SessionRowTime.working(rows[0], now: now), "12 min")
        XCTAssertEqual(SessionRowTime.working(rows[1], now: now), "now")
        XCTAssertEqual(SessionRowTime.working(rows[2], now: now), "now")
        XCTAssertEqual(SessionRowTime.status(rows[0], now: now), "Working 12 min")
        XCTAssertTrue(SessionRowTime.status(rows[3], now: now).hasPrefix("Idle · 5"), SessionRowTime.status(rows[3], now: now))
    }

    func testAProjectRouteNeverReadsAsASession() {
        let route = ConversationRoute.project(Self.projectID)
        XCTAssertEqual(ConversationRoute.projectID(route), Self.projectID)
        XCTAssertNil(ConversationRoute.projectID("one::session"))
        XCTAssertNil(ConversationRoute.projectID(Self.projectID))
    }
}
