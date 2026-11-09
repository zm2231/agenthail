@testable import AgenthailHelpers
import Foundation

@main
struct SidebarSectionsTest {
    static func session(_ id: String, project: String, status: String = "idle", parent: String? = nil) -> SessionState {
        var row = SessionState(id: id, surface: "claude", name: id, alias: nil, status: status, lastActive: "2026-10-23T08:00:00Z", queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(respond: true), readOnly: false, readOnlyReason: "")
        row.project = ProjectIdentity(id: project, displayName: project, rootPath: "/\(project)", kind: "git")
        row.subagent = parent.map { SubagentIdentity(parentId: $0, rootId: $0, depth: 1) }
        return row
    }

    static func listing(_ sessions: [SessionState], attention: [AttentionState] = []) -> DashboardSnapshot {
        DashboardSnapshot(updatedAt: "2026-10-23T08:00:00Z", eventCursor: 1, hostEpoch: "h", catalogSeq: 1, daemon: DaemonState(running: true, pid: 1, stale: false, refreshError: nil), surfaces: [], sessions: sessions, totalSessions: sessions.count, queue: [], channels: [], relays: [], history: [], attention: attention, deliveryProblems: nil, codexRecentHours: 5, busyDelivery: "queue")
    }

    static func family(_ id: String, project: String, members: [String] = []) -> ViewFamily {
        ViewFamily(id: id, rootId: id, projectId: project, checkoutId: "c-\(project)", members: ([id] + members).enumerated().map { ViewMember(id: $1, depth: $0 == 0 ? 0 : 1, contextOnly: false, pinned: false) }, pinned: false, workingSubagents: 0)
    }

    static func view() -> ScopeSessions {
        let counts = ["running", "current", "recent", "all", "archived"].reduce(into: [String: ScopePage.Count]()) { $0[$1] = ScopePage.Count(families: 3, sessions: 4) }
        let a = [family("a1", project: "a", members: ["a1-child"]), family("a2", project: "a")]
        let b = [family("b1", project: "b")]
        let projects = [ViewProject(id: "a", name: "a", counts: ScopePage.Count(families: 2, sessions: 3), collapsedFamilyIds: ["a1"]), ViewProject(id: "b", name: "b", counts: ScopePage.Count(families: 1, sessions: 1), collapsedFamilyIds: ["b1"])]
        return ScopeSessions(sessions: [], hostEpoch: "h", catalogSeq: 1, view: ScopePage.View(counts: counts, surfaces: [], projects: projects, families: a + b))
    }

    @MainActor
    static func keys(_ model: AgenthailModel, expanded: Set<String> = [], selected: String? = nil, families: Set<String> = [], shownProject: String? = nil) -> [String: SidebarSectionKey] {
        Dictionary(uniqueKeysWithValues: model.sessionTree.projects.map { ($0.id, SidebarSectionKey(project: $0, expanded: expanded.contains($0.id), selectedID: selected, attention: model.sessionTree.attention, expandedFamilies: families, shownProjectID: shownProject)) })
    }

    @MainActor
    static func familyKey(_ key: SidebarSectionKey?, _ id: String) -> SidebarFamilyKey? {
        guard let key, let family = key.project.checkouts.flatMap(\.families).first(where: { $0.id == id }) else { return nil }
        return key.family(family)
    }

    @MainActor
    static func main() {
        let model = AgenthailModel(connecting: false)
        model.snapshot = listing([session("a1", project: "a"), session("a1-child", project: "a", parent: "a1"), session("a2", project: "a"), session("b1", project: "b")])
        model.adoptCurrentView(view())
        let before = keys(model)
        check(Set(before.keys) == ["a", "b"], "both projects have a section: \(before.keys)")

        model.snapshot?.sessions[3] = session("b1", project: "b", status: "busy")
        let afterB = keys(model)
        check(afterB["a"] == before["a"], "a row changing in one project leaves the other project's section alone")
        check(afterB["b"] == before["b"], "a row changing in place leaves its own project's section alone too")
        check(familyKey(afterB["b"], "b1")?.family.root.status == "busy", "the row redraws from its own cell")

        model.snapshot?.sessions[2] = session("a2", project: "a", status: "busy")
        let afterA2 = keys(model, expanded: ["a"])
        check(familyKey(afterA2["a"], "a1") == familyKey(keys(model, expanded: ["a"])["a"], "a1"), "family keys are stable for an unchanged family")
        check(familyKey(afterA2["a"], "a1") == familyKey(SidebarSectionKey(project: before["a"]!.project, expanded: true, selectedID: nil, attention: [:], expandedFamilies: [], shownProjectID: nil), "a1"), "a sibling family changing leaves a family's rows alone")

        let opened = keys(model, shownProject: "a")
        check(opened["a"]?.isShownProject == true && opened["a"] != keys(model)["a"], "opening a project with nothing selected in it redraws its header")
        check(opened["b"] == keys(model)["b"], "opening a project leaves the others alone")
        let moved = keys(model, shownProject: "b")
        check(moved["a"]?.isShownProject == false && moved["a"] != opened["a"] && moved["b"]?.isShownProject == true, "opening another project moves the header mark")

        check(keys(model, expanded: ["a"])["a"] != keys(model)["a"], "expanding a project redraws it")
        check(keys(model)["a"]?.shown.checkouts.flatMap(\.families).map(\.id) == ["a1"], "collapsed shows the daemon's collapsed families")
        check(keys(model)["a"]?.moreLabel == "Show 1 more", "collapsed offers the hidden family: \(keys(model)["a"]?.moreLabel ?? "none")")

        let selectedHidden = keys(model, selected: "a2")
        check(selectedHidden["a"]?.selectedID == "a2", "selecting a hidden session reaches its project")
        check(selectedHidden["a"]?.shown.checkouts.flatMap(\.families).map(\.id) == ["a1", "a2"], "a collapsed project keeps the selected family in place")
        check(selectedHidden["b"] == keys(model)["b"], "selecting in one project leaves the other alone")
        check(familyKey(selectedHidden["a"], "a1")?.selectedID == nil && familyKey(selectedHidden["a"], "a2")?.selectedID == "a2", "only the selected family carries the selection")

        let child = keys(model, selected: "a1-child")
        check(familyKey(child["a"], "a1")?.expanded == true, "selecting a subagent opens its family")
        check(familyKey(keys(model, families: ["a1"])["a"], "a1")?.expanded == true && familyKey(keys(model)["a"], "a1")?.expanded == false, "toggling a family opens and closes it")

        let question = AttentionState(id: 1, sessionId: "a2", target: "a2", queueId: 0, reason: "Waiting on you", requestedAction: "answer", createdAt: "2026-10-23T08:00:00Z")
        let quiet = keys(model, expanded: ["a"])
        model.snapshot = listing(model.snapshot?.sessions ?? [], attention: [question])
        let asked = keys(model, expanded: ["a"])
        check(familyKey(asked["a"], "a2")?.attention["a2"] != nil && familyKey(quiet["a"], "a2")?.attention["a2"] == nil, "a question reaches its family")
        check(familyKey(asked["a"], "a1") == familyKey(quiet["a"], "a1"), "a question for one family leaves the others alone")
        print("sidebar sections: a change redraws only its own project and family, and expanding, selection and questions still reach them")
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
