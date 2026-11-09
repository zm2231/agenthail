@testable import AgenthailHelpers
import Foundation
import Observation

@MainActor
final class Fired {
    var hit = false
}

@main
struct BackgroundUpsertTest {
    static func session(_ id: String, status: String, lastActive: String = "2026-10-23T08:00:00Z") -> SessionState {
        var row = SessionState(id: id, surface: "claude", name: id, alias: nil, status: status, lastActive: lastActive, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(respond: true), readOnly: false, readOnlyReason: "")
        row.placement = "p-\(id)"
        return row
    }

    static func upsert(_ row: SessionState, seq: UInt64) -> CatalogStreamEvent {
        let json = String(decoding: try! JSONEncoder().encode(row), as: UTF8.self)
        return try! JSONDecoder().decode(CatalogStreamEvent.self, from: Data(#"{"stream":"catalog","seq":\#(seq),"type":"session.upserted","data":{"session":\#(json)}}"#.utf8))
    }

    @MainActor
    static func fires(_ read: @escaping @MainActor () -> Void, when change: () -> Void) -> Bool {
        let fired = Fired()
        withObservationTracking { read() } onChange: { MainActor.assumeIsolated { fired.hit = true } }
        change()
        return fired.hit
    }

    @MainActor
    static func main() {
        let model = AgenthailModel(connecting: false)
        model.snapshot = DashboardSnapshot(updatedAt: "2026-10-23T08:00:00Z", eventCursor: 1, hostEpoch: "h", catalogSeq: 1, daemon: DaemonState(running: true, pid: 1, stale: false, refreshError: nil), surfaces: [], sessions: [session("streaming", status: "busy"), session("other", status: "idle")], totalSessions: 2, queue: [], channels: [], relays: [], history: [], attention: [], deliveryProblems: nil, codexRecentHours: 5, busyDelivery: "queue")

        let families = ["streaming", "other"].map { ViewFamily(id: $0, rootId: $0, projectId: "proj", checkoutId: "c", members: [ViewMember(id: $0, depth: 0, contextOnly: false, pinned: false)], pinned: false, workingSubagents: 0) }
        let counts = ["running", "current", "recent", "all", "archived"].reduce(into: [String: ScopePage.Count]()) { $0[$1] = ScopePage.Count(families: 2, sessions: 2) }
        model.adoptCurrentView(ScopeSessions(sessions: [], hostEpoch: "h", catalogSeq: 1, view: ScopePage.View(counts: counts, surfaces: [], projects: [ViewProject(id: "proj", name: "proj", counts: ScopePage.Count(families: 2, sessions: 2), collapsedFamilyIds: ["streaming", "other"])], families: families)))
        check(model.sessionTree.projects.first?.checkouts.first?.families.map(\.id) == ["streaming", "other"], "the list shows both sessions")
        check(model.menuSessions.working == ["streaming"] && model.menuSessions.recent == ["other"], "the menu lists the busy session under Working: \(model.menuSessions)")

        var seq: UInt64 = 1
        func stamp(_ second: Int) -> String { "2026-10-23T09:\(String(format: "%02d", second / 60)):\(String(format: "%02d", second % 60))Z" }
        func progress(_ second: Int, status: String = "busy") -> [CatalogStreamEvent] {
            seq += 1
            return [upsert(session("streaming", status: status, lastActive: stamp(second)), seq: seq)]
        }
        let window: [(String, @MainActor () -> Void)] = [
            ("sessionTree", { _ = model.sessionTree }), ("subagentIDs", { _ = model.subagentIDs }),
            ("menuSessions", { _ = model.menuSessions }), ("refinementSurfaces", { _ = model.refinementSurfaces }),
            ("daemon", { _ = model.daemon }), ("projects", { _ = model.projects }), ("attention", { _ = model.attention }),
            ("queue", { _ = model.queue }), ("workingCount", { _ = model.workingCount }), ("sessionAttention", { _ = model.sessionAttention }),
            ("hasSnapshot", { _ = model.hasSnapshot }), ("deliveryProblems", { _ = model.deliveryProblems }),
        ]
        for (index, (name, read)) in window.enumerated() {
            let events = progress(index)
            check(!fires(read, when: { model.applyCatalogEvents(events) }), "a background row's progress leaves \(name) alone")
            check(model.session("streaming")?.lastActive == stamp(index), "and the progress landed while checking \(name)")
        }
        let otherEvents = progress(100)
        check(!fires({ _ = model.session("other") }, when: { model.applyCatalogEvents(otherEvents) }), "another session's cell is not told about a background row's progress")
        let ownEvents = progress(101)
        check(fires({ _ = model.session("streaming") }, when: { model.applyCatalogEvents(ownEvents) }), "the changed row's own cell is")
        check(model.session("streaming")?.lastActive == "2026-10-23T09:01:41Z", "the row shows the new progress")
        check(model.sessionTree.projects.first?.checkouts.first?.families.first?.root.lastActive == "2026-10-23T09:01:41Z", "and so does the list's row")

        let idleEvents = progress(102, status: "idle")
        check(fires({ _ = model.workingCount }, when: { model.applyCatalogEvents(idleEvents) }), "a busy session going idle changes the working count")
        check(model.workingCount == 0, "and the count is right")
        check(model.menuSessions.working.isEmpty && model.menuSessions.recent == ["streaming", "other"], "the menu moves it from Working to Recent: \(model.menuSessions)")

        model.holdOrder(true)
        model.snapshot?.sessions.removeAll { $0.id == "other" }
        check(model.sessionTree.projects.first?.checkouts.first?.families.map(\.id) == ["streaming", "other"], "a held list keeps a removed row")
        let heldEvents = progress(103)
        check(!fires({ _ = model.sessionTree }, when: { model.applyCatalogEvents(heldEvents) }), "a background row's progress leaves a held list alone too")
        model.holdOrder(false)
        check(model.sessionTree.projects.first?.checkouts.first?.families.map(\.id) == ["streaming"], "letting go drops the removed row")

        check(SessionNames.display("Fix the parser") == "Fix the parser" && SessionNames.display("https://github.com/acme/widgets/pull/1") == "acme / widgets", "the name check still shortens links and leaves plain names")
        print("ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
