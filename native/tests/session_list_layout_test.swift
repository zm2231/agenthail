@testable import AgenthailHelpers
import Foundation

@main
struct SessionListLayoutTest {
    static let caps = Capabilities(send: true, stream: true, reply: true, goal: false, compact: false, model: true, interrupt: true, steer: true)

    static func session(_ id: String, project: String) -> SessionState {
        SessionState(id: id, surface: "codex", name: id, alias: nil, status: "idle", lastActive: "2026-10-04T11:00:00Z", queueCount: 0, open: true, current: true, currentReason: nil, capabilities: caps, readOnly: nil, readOnlyReason: nil, cwd: "/repo", project: ProjectIdentity(id: project, displayName: project, rootPath: "/repo", kind: "git"), checkout: CheckoutIdentity(id: "main", path: "/repo", branch: "main", detachedHead: nil, isMain: true, dirty: false))
    }

    static func family(_ id: String, project: String, members: [String] = []) -> ViewFamily {
        ViewFamily(id: id, rootId: id, projectId: project, checkoutId: "", members: [ViewMember(id: id, depth: 0, contextOnly: false, pinned: false)] + members.map { ViewMember(id: $0, depth: 1, contextOnly: false, pinned: false) }, pinned: false, workingSubagents: 0)
    }

    static func main() {
        let families = [family("a", project: "p1"), family("b", project: "p2", members: ["b1"]), family("c", project: "p1"), family("ghost", project: "p2")]
        let view = ScopePage.View(counts: [:], surfaces: [], projects: ["p1", "p2"].map { ViewProject(id: $0, name: $0, counts: ScopePage.Count(families: 2, sessions: 2), collapsedFamilyIds: []) }, families: families)
        let rows = Dictionary(uniqueKeysWithValues: [session("a", project: "p1"), session("b", project: "p2"), session("b1", project: "p2"), session("c", project: "p1")].map { ($0.id, $0) })
        let tree = SessionTree.ordered(list: view, current: nil, rows: rows, attention: [:])
        check(tree.flat.map(\.id) == ["a", "b", "c"], "the flat list keeps the daemon's order across projects and skips a family without a row: \(tree.flat.map(\.id))")
        check(tree.projects.flatMap { $0.checkouts.flatMap(\.families) }.map(\.id) == ["a", "c", "b"], "the grouped list still groups by project")
        check(tree.flat.first { $0.id == "b" }?.members.map(\.id) == ["b1"], "a flat family keeps its subagents")
        check(SessionTree.empty.flat.isEmpty, "an empty tree has no flat families")

        check(SessionListLayout.order(tree.flat, expanded: { _ in false }) == ["a", "b", "c"], "keyboard order follows the flat list")
        check(SessionListLayout.order(tree.flat, expanded: { $0.id == "b" }) == ["a", "b", "b1", "c"], "an expanded family adds its subagents after it")
        check(SessionListLayout.order(tree.flat + tree.flat, expanded: { _ in false }) == ["a", "b", "c"], "a session listed twice is stepped through once")

        let refined = SessionRefinement(status: .working, surface: "claude")
        check(SessionListLayout.archivedRequest(filter: .all, refinement: SessionRefinement(), shows: false) == nil, "archived sessions stay out unless asked for")
        for filter in [SessionFilter.running, .recent, .archived] {
            check(SessionListLayout.archivedRequest(filter: filter, refinement: SessionRefinement(), shows: true) == nil, "\(filter.rawValue) never mixes in archived sessions")
        }
        let request = SessionListLayout.archivedRequest(filter: .all, refinement: refined, shows: true)
        check(request == ScopeRequest(scope: "archived", surface: "claude", status: SessionStatusFilter.working.rawValue), "All with Show Archived reads the archived listing under the same filters: \(String(describing: request))")
        print("ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
