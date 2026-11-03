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
        XCTAssertTrue(reads.allSatisfy { $0.contains("projectId=p-widget") }, "Earlier was not read for the project: \(reads)")
        XCTAssertTrue(reads.last?.contains("cursor=1") == true)
    }

    func testAProjectRouteNeverReadsAsASession() {
        let route = ConversationRoute.project(Self.projectID)
        XCTAssertEqual(ConversationRoute.projectID(route), Self.projectID)
        XCTAssertNil(ConversationRoute.projectID("one::session"))
        XCTAssertNil(ConversationRoute.projectID(Self.projectID))
    }
}
