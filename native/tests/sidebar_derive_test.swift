@testable import AgenthailHelpers
import Foundation

@main
struct SidebarDeriveTest {
    @MainActor
    static func main() {
        func session(_ id: String, status: String) -> SessionState {
            SessionState(id: id, surface: "claude", name: id, alias: nil, status: status, lastActive: "2026-10-23T08:00:00Z", queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(respond: true), readOnly: false, readOnlyReason: "")
        }
        func listing(_ sessions: [SessionState], attention: [AttentionState] = []) -> DashboardSnapshot {
            DashboardSnapshot(updatedAt: "2026-10-23T08:00:00Z", eventCursor: 1, hostEpoch: "h", catalogSeq: 1, daemon: DaemonState(running: true, pid: 1, stale: false, refreshError: nil), surfaces: [], sessions: sessions, totalSessions: sessions.count, queue: [], channels: [], relays: [], history: [], attention: attention, deliveryProblems: nil, codexRecentHours: 5, busyDelivery: "queue")
        }
        func shown(_ model: AgenthailModel) -> [String] {
            model.sessionTree.projects.flatMap { $0.checkouts.flatMap { $0.families.map(\.root.id) } }.sorted()
        }

        let model = AgenthailModel(connecting: false)
        check(model.knownSessions.isEmpty && shown(model).isEmpty, "a model without a catalog lists nothing")

        model.snapshot = listing([session("a", status: "idle"), session("b", status: "busy")])
        check(model.knownSessions.map(\.id) == ["a", "b"] && model.sessionFamilies.count == 2, "a new catalog replaces the listed sessions and families")
        check(shown(model) == ["a", "b"], "the tree follows a new catalog: \(shown(model))")

        model.sessionFilter = .running
        check(shown(model) == ["b"], "the tree follows the filter: \(shown(model))")

        model.snapshot?.sessions.append(session("c", status: "busy"))
        check(shown(model) == ["b", "c"] && model.knownSessions.count == 3, "the tree follows a change made inside the catalog: \(shown(model))")

        let question = AttentionState(id: 1, sessionId: "a", target: "a", queueId: 0, reason: "Waiting on you", requestedAction: "answer", createdAt: "2026-10-23T08:00:00Z")
        model.snapshot = listing([session("a", status: "idle"), session("b", status: "busy")], attention: [question])
        check(model.sessionAttention["a"] != nil && model.sessionTree.needsYou.map(\.id) == ["a"], "attention and Needs you follow the catalog: \(model.sessionTree.needsYou.map(\.id))")

        model.sessionRefinement = SessionRefinement(surface: "codex")
        check(shown(model).isEmpty, "the tree follows the refinement: \(shown(model))")

        model.snapshot = nil
        check(model.knownSessions.isEmpty && model.sessionAttention.isEmpty && shown(model).isEmpty, "losing the catalog clears what the sidebar shows")
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
