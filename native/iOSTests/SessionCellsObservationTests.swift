import XCTest
@testable import Agenthail

final class SessionCellsObservationTests: XCTestCase {
    private static func row(_ id: String) -> SessionState {
        var row = SessionState(id: id, surface: "claude", name: id, alias: nil, status: "idle", lastActive: "2026-11-08T10:00:00Z", queueCount: 0,
                               open: true, current: false, currentReason: nil, capabilities: Capabilities(send: true, steer: true), readOnly: false, readOnlyReason: nil)
        row.project = ProjectIdentity(id: "widget", displayName: "widget", rootPath: "/w", kind: "git")
        return row
    }

    private static func view(_ ids: [String]) -> ScopePage.View {
        let count = ScopePage.Count(families: ids.count, sessions: ids.count)
        let families = ids.map { ViewFamily(id: $0, rootId: $0, projectId: "widget", checkoutId: "", members: [ViewMember(id: $0, depth: 0, contextOnly: false, pinned: false)], pinned: false, workingSubagents: 0) }
        return ScopePage.View(counts: ["current": count, "all": count], surfaces: [],
                              projects: [ViewProject(id: "widget", name: "widget", counts: count, collapsedFamilyIds: ids)], families: families)
    }

    @MainActor
    private func makeModel(_ ids: [String]) -> AgenthailIOSModel {
        let model = AgenthailIOSModel(api: AgenthailAPI(baseURL: URL(string: "https://preview.invalid")!, token: "test"), paired: true)
        model.setHostSnapshot(HostCatalogTests.snapshot(ids.map(Self.row)), view: Self.view(ids))
        return model
    }

    @MainActor
    private func familyIDs(_ model: AgenthailIOSModel) -> [String] {
        model.sessionTree(scope: .recent, search: "").projects.flatMap { $0.checkouts.flatMap(\.families) }.map(\.id)
    }

    @MainActor
    func testAContentChangeInOneSessionTellsOnlyThatSessionsReaders() throws {
        let model = makeModel(["a", "b"])
        XCTAssertEqual(familyIDs(model), ["computer::a", "computer::b"])
        let changed = ObservationProbe { _ = model.session("computer::a") }
        let other = ObservationProbe { _ = model.session("computer::b") }
        let list = ObservationProbe { _ = model.sessionTree(scope: .recent, search: "") }
        let parts = ObservationProbe {
            _ = model.queue
            _ = model.listing
            _ = model.sessionCount
            _ = model.usageSessions
            _ = model.waitingSessions
            _ = model.attention
            _ = model.sessionAttention
            _ = model.isPaired
            _ = model.showsHostNames
            _ = model.projects
            _ = model.hosts
            _ = model.decisionsKey
        }
        let revision = model.listRevision

        try XCTUnwrap(model.connection("computer")).updateSnapshot { snapshot in
            snapshot.sessions[0].queueCount = 2
        }

        XCTAssertEqual(model.session("computer::a")?.queueCount, 2)
        XCTAssertTrue(changed.changed(), "the changed session's readers are told")
        XCTAssertFalse(other.changed(), "another session's readers are not")
        XCTAssertFalse(list.changed(), "the list tree is not")
        XCTAssertFalse(parts.changed(), "the list's other parts are not")
        XCTAssertEqual(model.listRevision, revision)
    }

    @MainActor
    func testAStructuralChangeRederivesTheTreeOnce() {
        let model = makeModel(["a", "b"])
        let list = ObservationProbe { _ = model.sessionTree(scope: .recent, search: "") }
        let revision = model.listRevision

        model.setHostSnapshot(HostCatalogTests.snapshot(["a", "b", "c"].map(Self.row)), view: Self.view(["a", "b", "c"]))

        XCTAssertTrue(list.changed())
        XCTAssertEqual(model.listRevision, revision + 1, "the snapshot and the order changed in one rebuild, and the tree was published once")
        XCTAssertEqual(familyIDs(model), ["computer::a", "computer::b", "computer::c"])
    }
}
