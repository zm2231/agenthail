@testable import AgenthailHelpers
import Foundation
import Observation

@MainActor
final class Notified {
    var hit = false
}

@main
struct SidebarDeriveTest {
    static func session(_ id: String, status: String, parent: String? = nil, placement: String = "p") -> SessionState {
        var row = SessionState(id: id, surface: "claude", name: id, alias: nil, status: status, lastActive: "2026-10-23T08:00:00Z", queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(respond: true), readOnly: false, readOnlyReason: "")
        row.project = ProjectIdentity(id: parent == nil ? "proj-\(id.prefix(1))" : "proj-a", displayName: "project", rootPath: "/p", kind: "git")
        row.subagent = parent.map { SubagentIdentity(parentId: $0, rootId: $0, depth: 1) }
        row.placement = placement
        return row
    }

    static func listing(_ sessions: [SessionState], attention: [AttentionState] = [], epoch: String = "h") -> DashboardSnapshot {
        DashboardSnapshot(updatedAt: "2026-10-23T08:00:00Z", eventCursor: 1, hostEpoch: epoch, catalogSeq: 1, daemon: DaemonState(running: true, pid: 1, stale: false, refreshError: nil), surfaces: [], sessions: sessions, totalSessions: sessions.count, queue: [], channels: [], relays: [], history: [], attention: attention, deliveryProblems: nil, codexRecentHours: 5, busyDelivery: "queue")
    }

    static func family(_ id: String, project: String, members: [String] = [], pinned: Bool = false) -> ViewFamily {
        ViewFamily(id: id, rootId: id, projectId: project, checkoutId: "c-\(project)", members: ([id] + members).enumerated().map { ViewMember(id: $1, depth: $0 == 0 ? 0 : 1, contextOnly: false, pinned: pinned) }, pinned: pinned, workingSubagents: 0)
    }

    static func view(_ projects: [(String, [ViewFamily], [String])], seq: UInt64) -> ScopeSessions {
        let counts = ["running", "current", "recent", "all", "archived"].reduce(into: [String: ScopePage.Count]()) { $0[$1] = ScopePage.Count(families: 2, sessions: 3) }
        let view = ScopePage.View(counts: counts, surfaces: [], projects: projects.map { ViewProject(id: $0.0, name: $0.0, counts: ScopePage.Count(families: $0.1.count, sessions: $0.1.count), collapsedFamilyIds: $0.2) }, families: projects.flatMap(\.1))
        return ScopeSessions(sessions: [], hostEpoch: "h", catalogSeq: seq, view: view)
    }

    @MainActor
    static func shown(_ model: AgenthailModel) -> [String] {
        model.sessionTree.projects.flatMap { project in project.checkouts.flatMap { $0.families.map { "\(project.id)/\($0.root.id)" } } }
    }

    @MainActor
    static func main() {
        let model = AgenthailModel(connecting: false)
        check(model.knownSessions.isEmpty && shown(model).isEmpty, "a model without a catalog lists nothing")

        model.snapshot = listing([session("a", status: "idle"), session("b", status: "busy"), session("a-child", status: "busy", parent: "a")])
        check(shown(model).isEmpty, "rows alone are not an order: the list waits for the daemon's view: \(shown(model))")

        model.adoptCurrentView(view([("proj-b", [family("b", project: "proj-b", pinned: true)], ["b"]), ("proj-a", [family("a", project: "proj-a", members: ["a-child"])], ["a"])], seq: 1))
        check(shown(model) == ["proj-b/b", "proj-a/a"], "projects and families render in the daemon's order, not by name or time: \(shown(model))")
        check(model.sessionTree.projects.last?.checkouts.first?.families.first?.members.map(\.id) == ["a-child"], "members come from the view")
        check(model.sessionTree.counts[.all] == 2, "counts come from the daemon")

        model.snapshot?.sessions[1] = session("b", status: "idle")
        check(shown(model) == ["proj-b/b", "proj-a/a"], "a row changing in place does not move it: \(shown(model))")
        check(model.sessionTree.projects.first?.checkouts.first?.families.first?.root.status == "idle", "the row itself shows its new state")

        model.adoptCurrentView(view([("proj-a", [family("a", project: "proj-a")], ["a"]), ("proj-b", [family("b", project: "proj-b")], ["b"])], seq: 0))
        check(shown(model) == ["proj-b/b", "proj-a/a"], "an older view never replaces a newer one: \(shown(model))")

        model.holdOrder(true)
        model.adoptCurrentView(view([("proj-a", [family("a", project: "proj-a")], ["a"]), ("proj-b", [family("b", project: "proj-b")], ["b"])], seq: 2))
        check(shown(model) == ["proj-b/b", "proj-a/a"], "a held list keeps its order: \(shown(model))")
        model.snapshot?.sessions.removeAll { $0.id == "b" }
        check(shown(model) == ["proj-b/b", "proj-a/a"], "a held list keeps a removed row in place")
        let release = Notified()
        withObservationTracking { _ = model.sessionTree } onChange: { MainActor.assumeIsolated { release.hit = true } }
        model.holdOrder(false)
        check(shown(model) == ["proj-a/a"], "releasing the hold shows the new order and drops the removed row: \(shown(model))")
        check(release.hit, "releasing the hold reaches views that read the sidebar")

        let question = AttentionState(id: 1, sessionId: "a-child", target: "a-child", queueId: 0, reason: "Waiting on you", requestedAction: "answer", createdAt: "2026-10-23T08:00:00Z")
        model.snapshot = listing([session("a", status: "idle"), session("a-child", status: "busy", parent: "a"), session("c", status: "busy")], attention: [question])
        model.adoptCurrentView(view([("proj-c", [family("c", project: "proj-c")], ["c"]), ("proj-a", [family("a", project: "proj-a", members: ["a-child"])], ["a"])], seq: 3))
        check(model.sessionTree.needsYou.map(\.id) == ["a-child"], "Needs you follows the view's members: \(model.sessionTree.needsYou.map(\.id))")

        model.adoptCurrentView(view([("proj-c", [family("c", project: "proj-c"), family("c2", project: "proj-c"), family("c3", project: "proj-c")], ["c"])], seq: 4))
        model.snapshot?.sessions += [session("c2", status: "idle"), session("c3", status: "idle")]
        let project = model.sessionTree.projects[0]
        check(project.collapsed(keeping: nil).checkouts.flatMap(\.families).map(\.id) == ["c"], "collapse shows exactly the daemon's collapsed families")
        check(project.collapsed(keeping: "c3").checkouts.flatMap(\.families).map(\.id) == ["c", "c3"], "a selected family stays in its daemon place")

        model.adoptRefreshed(listing([session("c", status: "busy")], epoch: "h2"))
        var stale = view([("proj-z", [family("z", project: "proj-z")], ["z"])], seq: 99)
        stale.hostEpoch = "h"
        model.adoptCurrentView(stale)
        check(!shown(model).contains("proj-z/z"), "a Current view from the epoch before a restart never replaces the newer one: \(shown(model))")
        var current = view([("proj-c", [family("c", project: "proj-c")], ["c"])], seq: 1)
        current.hostEpoch = "h2"
        model.adoptCurrentView(current)
        check(shown(model) == ["proj-c/c"], "the restarted daemon's view is taken: \(shown(model))")

        model.sessionFilter = .running
        check(shown(model).isEmpty, "another filter shows nothing until its own view arrives, never the Current order first")
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
