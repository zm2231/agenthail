@testable import AgenthailHelpers
import Foundation

@main
struct SessionStoreTest {
    @MainActor
    static func main() {
        func detail(_ id: String, items: [(String, String)] = [("a", "a1")], nextBefore: Int64? = 40) -> SessionDetail {
            let rows = items.map { #"{"id":"\#($0.0)","kind":"message","role":"assistant","title":"message","text":"\#($0.1)","truncated":false}"# }.joined(separator: ",")
            let before = nextBefore.map(String.init) ?? "null"
            let json = #"{"session":{"id":"\#(id)","surface":"claude","name":"n","status":"idle","lastActive":"2026-10-04T12:00:00Z"},"journalSeq":100,"exchanges":[],"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true},"readOnly":false,"readOnlyReason":"","timeline":{"items":[\#(rows)],"nextBefore":\#(before),"truncated":false}}"#
            return try! JSONDecoder().decode(SessionDetail.self, from: Data(json.utf8))
        }
        func item(_ id: String, _ text: String) -> TimelineItem {
            TimelineItem(id: id, kind: "message", role: "assistant", title: "message", text: text, timestamp: nil, callId: nil, status: nil, truncated: false, truncationReason: nil, bodyRef: nil)
        }

        let store = SessionStore(limits: SessionStore.Limits(sessions: 3, bytes: 1 << 20, sessionBytes: 1 << 20))
        var pinned: Set<String> = []
        var working: Set<String> = []
        var clock = Date(timeIntervalSince1970: 1_000_000)
        store.pinned = { pinned.contains($0) }
        store.working = { working.contains($0) }
        store.now = { clock }

        store.keep(WarmSession(detail: detail("A"), cursor: 100), for: "A")
        check(store.open("A")?.cursor == 100, "a kept session reopens at its journal position")
        check(store.open("missing") == nil, "a session never shown is not cached")

        pinned = ["A"]
        store.keep(WarmSession(detail: detail("B"), cursor: 1), for: "B")
        store.keep(WarmSession(detail: detail("C"), cursor: 1), for: "C")
        store.keep(WarmSession(detail: detail("D"), cursor: 1), for: "D")
        check(store.ids == ["A", "C", "D"], "past the count limit the least recent session goes: \(store.ids)")
        check(store.peek("A") != nil && store.peek("B") == nil, "a shown session stays even when it is the least recent: \(store.ids)")

        pinned = []
        working = ["C"]
        _ = store.open("C")
        _ = store.open("A")
        _ = store.open("D")
        store.keep(WarmSession(detail: detail("E"), cursor: 1), for: "E")
        check(store.peek("C") != nil && store.peek("A") == nil, "a working session opened in the last ten minutes stays: \(store.ids)")
        clock = clock.addingTimeInterval(SessionStore.workingPin + 1)
        store.keep(WarmSession(detail: detail("F"), cursor: 1), for: "F")
        check(store.peek("C") == nil, "a working session opened long ago can go: \(store.ids)")
        working = []

        let big = String(repeating: "x", count: 4096)
        let bounded = SessionStore(limits: SessionStore.Limits(sessions: 16, bytes: 20_000, sessionBytes: 9_000))
        bounded.keep(WarmSession(detail: detail("P", items: [("p", big)], nextBefore: 7), older: [item("o1", big), item("o2", big)], olderCursor: 3, cursor: 1), for: "P")
        check(bounded.peek("P")?.older.isEmpty == true && bounded.peek("P")?.olderCursor == 7, "a session past its own byte bound drops its older pages and pages back from its shown rows")
        bounded.keep(WarmSession(detail: detail("Q", items: [("q", big)]), cursor: 1), for: "Q")
        bounded.keep(WarmSession(detail: detail("R", items: [("r", big)]), cursor: 1), for: "R")
        bounded.keep(WarmSession(detail: detail("S", items: [("s", big)]), cursor: 1), for: "S")
        bounded.keep(WarmSession(detail: detail("T", items: [("t", big)]), cursor: 1), for: "T")
        check(bounded.peek("P") == nil && bounded.peek("T") != nil && bounded.ids.count == 4, "past the total byte bound the least recent sessions go: \(bounded.ids)")

        let carried = SessionStore(limits: .desktop)
        carried.keep(WarmSession(detail: detail("session_old"), cursor: 100), for: "session_old")
        carried.keep(WarmSession(detail: detail("kept"), cursor: 5), for: "kept")
        var rekeys = SessionRekeys()
        rekeys.record(["session_old": "conversation-a"])
        carried.carry(rekeys)
        let moved = carried.peek("conversation-a")
        check(moved?.detail.session.id == "conversation-a" && moved?.detail.timeline?.items.map(\.id) == ["a"], "a rekeyed session keeps its rows under the id it continues as")
        check(moved?.cursor == nil && carried.peek("kept")?.cursor == 5, "a rekeyed session reads a page before resuming, since its cursor counted another journal")
        check(carried.peek("session_old") == nil && Set(carried.ids) == ["conversation-a", "kept"], "the retired id is gone: \(carried.ids)")
        carried.prune(keeping: ["kept"])
        check(carried.ids == ["kept"], "sessions that no longer exist are dropped: \(carried.ids)")

        var timeline = detail("O", items: [("a", "a1"), ("b", "b1"), ("c", "c1")]).timeline!
        var older = [item("z", "z1")]
        for update in [item("a", "a2"), item("d", "d1"), item("z", "z2"), item("b", "b2")] {
            SessionStore.place(update, in: &timeline, older: &older)
        }
        check(timeline.items.map(\.id) == ["a", "b", "c", "d"] && timeline.items.map(\.text) == ["a2", "b2", "c1", "d1"] && older.map(\.text) == ["z2"], "an updated row keeps its place wherever it is shown, and a new row goes at the end: \(timeline.items.map(\.id))")

        var live = SessionState(id: "L", surface: "claude", name: "l", alias: nil, status: "busy", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(respond: true), readOnly: true, readOnlyReason: "elsewhere")
        live.waitingOn = nil
        let refreshed = SessionStore.live(detail("L"), live)
        check(refreshed.session.status == "busy" && refreshed.capabilities.respond && refreshed.readOnly && refreshed.readOnlyReason == "elsewhere", "a cached detail shows the session's current state")
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
