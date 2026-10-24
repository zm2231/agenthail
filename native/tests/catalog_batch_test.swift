@testable import AgenthailHelpers
import Foundation

@main
struct CatalogBatchTest {
    static func session(_ id: String, _ status: String, name: String? = nil) -> String {
        #"{"id":"\#(id)","surface":"claude","name":"\#(name ?? id)","status":"\#(status)","queueCount":0,"open":true,"current":true,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":false,"interrupt":true,"steer":true}}"#
    }

    static func event(_ seq: UInt64, _ type: String, _ data: String) -> CatalogStreamEvent {
        try! JSONDecoder().decode(CatalogStreamEvent.self, from: Data(#"{"stream":"catalog","seq":\#(seq),"type":"\#(type)","data":\#(data)}"#.utf8))
    }

    static func upserted(_ seq: UInt64, _ id: String, _ status: String, name: String? = nil) -> CatalogStreamEvent {
        event(seq, "session.upserted", #"{"session":\#(session(id, status, name: name))}"#)
    }

    static func listing(_ sessions: [String]) -> DashboardSnapshot {
        try! JSONDecoder().decode(DashboardSnapshot.self, from: Data(#"{"updatedAt":"2026-10-23T21:00:00Z","hostEpoch":"h","catalogSeq":10,"daemon":{"running":true,"pid":1,"stale":false},"surfaces":[{"name":"claude","connected":true,"health":"healthy","capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":false,"interrupt":true,"steer":true}}],"sessions":[\#(sessions.joined(separator: ","))],"totalSessions":\#(sessions.count),"queue":[],"channels":[],"relays":[],"history":[],"attention":[],"codexRecentHours":5,"busyDelivery":"queue"}"#.utf8))
    }

    static func main() {
        let base = listing([session("a", "idle"), session("b", "idle")])

        let ordered = base.applyingCatalog([upserted(11, "a", "busy"), upserted(12, "a", "idle"), upserted(13, "b", "busy")], after: 10)
        check(ordered.cursor == 13 && ordered.changed && ordered.sessionsChanged, "a batch applies every event and ends at the last")
        check(ordered.snapshot.sessions.map(\.status) == ["idle", "busy"], "events apply in order, so the last one for a session wins: \(ordered.snapshot.sessions.map(\.status))")

        let skipped = base.applyingCatalog([upserted(9, "a", "busy", name: "old"), upserted(10, "a", "busy", name: "old"), upserted(11, "b", "busy")], after: 10)
        check(skipped.cursor == 11 && skipped.snapshot.sessions[0].status == "idle" && skipped.snapshot.sessions[0].name == "a", "events at or before the cursor are already in the snapshot and are skipped")

        let repeated = base.applyingCatalog([upserted(11, "a", "busy"), upserted(11, "a", "idle", name: "again")], after: 10)
        check(repeated.snapshot.sessions[0].status == "busy" && repeated.cursor == 11, "an event read twice applies once")

        let nothing = base.applyingCatalog([upserted(5, "a", "busy")], after: 10)
        check(!nothing.changed && nothing.cursor == 10, "a batch of old events changes nothing")

        let sessionStream = try! JSONDecoder().decode(CatalogStreamEvent.self, from: Data(#"{"stream":"session","seq":11,"type":"session.upserted","data":{"session":\#(session("a", "busy"))}}"#.utf8))
        let other = base.applyingCatalog([sessionStream], after: 10)
        check(!other.changed && other.cursor == 10, "events from another stream are ignored")

        let added = base.applyingCatalog([upserted(11, "c", "busy")], after: 10)
        check(added.snapshot.sessions.map(\.id) == ["a", "b", "c"] && added.snapshot.totalSessions == 3, "a new session is appended and counted")

        let queued = base.applyingCatalog([event(11, "session.queue", #"{"sessionId":"b","queueCount":3}"#)], after: 10)
        check(queued.snapshot.sessions[1].queueCount == 3 && queued.sessionsChanged, "a queue event sets the session's count")

        let removed = base.applyingCatalog([event(11, "session.removed", #"{"sessionId":"a","reason":"rekeyed","rekeyedTo":"a2"}"#), event(12, "session.removed", #"{"sessionId":"b","reason":"unlisted"}"#)], after: 10)
        check(removed.snapshot.sessions.isEmpty && removed.snapshot.totalSessions == 0, "removed sessions leave the snapshot")
        check(removed.removals == [CatalogRemoval(id: "a", unlisted: false, rekeyedTo: "a2"), CatalogRemoval(id: "b", unlisted: true, rekeyedTo: nil)], "removals are reported in order for their side effects: \(removed.removals)")

        let problems = (1...51).map { event(UInt64(10 + $0), "delivery.problem", #"{"deliveryId":\#($0),"sessionId":"a","message":"m","reason":"closed","at":"2026-10-23T21:00:00Z"}"#) }
        let capped = base.applyingCatalog(problems, after: 10)
        check(capped.snapshot.deliveryProblems?.count == 50 && capped.snapshot.deliveryProblems?.first?.deliveryId == 51, "delivery problems are newest first and capped like the snapshot's")
        let dismissed = capped.snapshot.applyingCatalog([event(62, "delivery.dismissed", #"{"deliveryId":51}"#)], after: 61)
        check(dismissed.snapshot.deliveryProblems?.first?.deliveryId == 50 && !dismissed.sessionsChanged, "a dismissed problem leaves")

        let health = base.applyingCatalog([event(11, "surface.health", #"{"surface":"claude","health":"unavailable","detail":"down"}"#)], after: 10)
        check(health.snapshot.surfaces[0].health == "unavailable" && !health.snapshot.surfaces[0].connected && !health.needsSnapshot, "a health event updates its surface")

        for (data, type) in [(#"{"surface":"codex","health":"unavailable"}"#, "surface.health"), (#"{}"#, "session.upserted"), (#"{}"#, "session.removed")] {
            let unknown = base.applyingCatalog([event(11, type, data), upserted(12, "a", "busy")], after: 10)
            check(unknown.needsSnapshot && unknown.cursor == 12 && unknown.snapshot.sessions[0].status == "busy", "a \(type) the snapshot cannot take asks for a fresh one and the rest still apply")
        }

        let calls = Counter()
        let inbox = CatalogInbox(bound: 3) { calls.add() }
        inbox.append(upserted(11, "a", "busy"))
        inbox.append(upserted(12, "a", "idle"))
        check(calls.value == 1, "the inbox asks for one drain until it is drained, got \(calls.value)")
        guard case .events(let first) = inbox.take() else { fail("the inbox holds events") }
        check(first.map(\.seq) == [11, 12], "the inbox keeps every event in order")
        guard case .events(let none) = inbox.take(), none.isEmpty else { fail("a drained inbox is empty") }
        inbox.append(upserted(13, "a", "busy"))
        check(calls.value == 2, "a drained inbox asks again for the next event")
        inbox.append(upserted(14, "a", "busy"))
        inbox.append(upserted(15, "a", "busy"))
        inbox.append(upserted(16, "a", "busy"))
        inbox.append(upserted(17, "a", "busy"))
        guard case .overflow = inbox.take() else { fail("an inbox past its bound reports an overflow") }
        guard case .overflow = inbox.take() else { fail("an overflowed inbox stays overflowed") }
        check(calls.value == 2, "an overflowed inbox asks for no more drains than it needs")
        print("catalog batch tests passed")
    }

    final class Counter: @unchecked Sendable {
        private let lock = NSLock()
        private var count = 0
        var value: Int { lock.withLock { count } }
        func add() { lock.withLock { count += 1 } }
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else { fail(message) }
    }

    static func fail(_ message: String) -> Never {
        print("FAIL: \(message)")
        exit(1)
    }
}
