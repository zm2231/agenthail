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

        let review = RuntimeCommand(name: "review", description: "Review a PR", argumentHint: "[pr]", insert: "/review")
        let listed = RuntimeCommands(commands: [review], unavailable: "")
        let commanded = SessionStore(limits: .desktop)
        let first = commanded.beginCommandsRead("A")
        check(first != nil && commanded.beginCommandsRead("A") == nil, "a session's commands are read once")
        commanded.commandsRead(listed, for: "A", read: first!)
        check(commanded.commands["A"] == listed, "a read seeds the session's commands")
        let racing = commanded.beginCommandsRead("B")!
        let remote = RuntimeCommands(commands: [], unavailable: "Commands run only when this session has Remote Control")
        commanded.commandsStreamed(remote, for: "B")
        commanded.commandsRead(listed, for: "B", read: racing)
        check(commanded.commands["B"] == remote, "a read that lands after a streamed list does not replace it")
        let olderHost = commanded.beginCommandsRead("C")!
        commanded.commandsRead(nil, for: "C", read: olderHost)
        check(commanded.commands["C"] == nil && commanded.beginCommandsRead("C") == nil, "a computer without the commands route leaves them unset and is not asked again")
        let flaky = commanded.beginCommandsRead("D")!
        commanded.commandsRead(nil, for: "D", read: flaky, retry: true)
        check(commanded.beginCommandsRead("D") != nil, "a read that failed for another reason is tried again on the next open")
        commanded.rereadCommands("B")
        let reread = commanded.beginCommandsRead("B")
        check(reread != nil && commanded.commands["B"] == remote, "a session moved to another route reads its commands again and keeps showing the old list until then")
        commanded.commandsRead(listed, for: "B", read: reread!)
        check(commanded.commands["B"] == listed, "the new route's commands replace the old ones")
        var commandRekeys = SessionRekeys()
        commandRekeys.record(["A": "conversation-a"])
        commanded.carry(commandRekeys)
        check(commanded.commands["conversation-a"] == listed && commanded.commands["A"] == nil, "a rekeyed session keeps its commands")
        commanded.prune(keeping: ["B"])
        check(commanded.commands.keys.sorted() == ["B"], "commands of sessions that no longer exist are dropped: \(commanded.commands.keys.sorted())")

        let replaced = try! JSONDecoder().decode(SessionStreamEvent.self, from: Data(#"{"stream":"session","sessionId":"B","seq":7,"type":"item","data":{"itemId":"state:commands","version":2,"op":"replace","kind":"commands","ts":"2026-10-23T08:00:00Z","truncated":false,"unavailable":"Commands run only when this session has Remote Control."}}"#.utf8))
        check(replaced.data.kind == "commands" && replaced.data.commands == nil && replaced.data.unavailable == "Commands run only when this session has Remote Control.", "a commands row that omits an empty list still decodes")
        let listedRow = try! JSONDecoder().decode(SessionStreamEvent.self, from: Data(#"{"stream":"session","sessionId":"B","seq":8,"type":"item","data":{"itemId":"state:commands","version":3,"op":"replace","kind":"commands","ts":"2026-10-23T08:00:01Z","truncated":false,"commands":[{"name":"review","description":"Review a PR","argumentHint":"[pr]","insert":"/review"}]}}"#.utf8))
        check(listedRow.data.commands == [review] && listedRow.data.unavailable == nil, "a commands row that omits an empty reason still decodes")

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
