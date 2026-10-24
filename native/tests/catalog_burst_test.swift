@testable import AgenthailHelpers
import Combine
import Foundation

@main
struct CatalogBurstTest {
    static func session(_ id: String, _ status: String, name: String? = nil) -> String {
        #"{"id":"\#(id)","surface":"claude","name":"\#(name ?? id)","status":"\#(status)","lastActive":"2026-10-23T21:00:00Z","queueCount":0,"open":true,"current":true,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":false,"interrupt":true,"steer":true}}"#
    }

    static func upserted(_ seq: UInt64, _ id: String, _ status: String, name: String? = nil) -> CatalogStreamEvent {
        try! JSONDecoder().decode(CatalogStreamEvent.self, from: Data(#"{"stream":"catalog","seq":\#(seq),"type":"session.upserted","data":{"session":\#(session(id, status, name: name))}}"#.utf8))
    }

    static func listing(_ sessions: [String], seq: UInt64) -> DashboardSnapshot {
        try! JSONDecoder().decode(DashboardSnapshot.self, from: Data(#"{"updatedAt":"2026-10-23T21:00:00Z","hostEpoch":"h","catalogSeq":\#(seq),"daemon":{"running":true,"pid":1,"stale":false},"surfaces":[],"sessions":[\#(sessions.joined(separator: ","))],"totalSessions":\#(sessions.count),"queue":[],"channels":[],"relays":[],"history":[],"attention":[],"codexRecentHours":5,"busyDelivery":"queue"}"#.utf8))
    }

    @MainActor
    static func working(_ model: AgenthailModel) -> [String] {
        model.sessionTree.projects.flatMap { $0.checkouts.flatMap { $0.families.map(\.root.id) } }.sorted()
    }

    @MainActor
    static func spin(until done: () -> Bool) -> Bool {
        var passes = 0
        while !done(), passes < 10_000 {
            RunLoop.main.run(until: Date().addingTimeInterval(0.005))
            passes += 1
        }
        return done()
    }

    @MainActor
    static func main() {
        snapshotsLandingMidBatch()
        burstKeepsTheSidebarCurrent()
        overflowReloadsInsteadOfApplyingTheBacklog()
        print("catalog burst tests passed")
    }

    @MainActor
    static func snapshotsLandingMidBatch() {
        let model = AgenthailModel(connecting: false)
        model.sessionFilter = .running
        model.adoptRefreshed(listing([session("a", "idle")], seq: 10))
        let inbox = model.catalogFeed.openInbox()
        inbox.append(upserted(11, "a", "busy"))
        inbox.append(upserted(12, "a", "idle"))
        model.adoptRefreshed(listing([session("a", "busy")], seq: 11))
        check(working(model) == ["a"], "a snapshot newer than everything applied is taken")
        check(spin(until: { working(model).isEmpty }), "the pending idle after that snapshot still applies: \(working(model))")

        model.adoptRefreshed(listing([session("a", "busy")], seq: 11))
        check(working(model).isEmpty, "a snapshot read before an applied idle keeps the session idle")

        inbox.append(upserted(13, "a", "busy", name: "skipped"))
        inbox.append(upserted(14, "a", "busy", name: "skipped"))
        model.adoptRefreshed(listing([session("a", "busy", name: "from snapshot")], seq: 14))
        inbox.append(upserted(15, "a", "idle", name: "after snapshot"))
        check(spin(until: { model.knownSessions.first?.name == "after snapshot" }), "events after a snapshot apply: \(model.knownSessions.map(\.name))")
        check(working(model).isEmpty, "the last event wins over the snapshot it followed")
    }

    @MainActor
    static func burstKeepsTheSidebarCurrent() {
        let ids = (0..<30).map { "s\($0)" }
        let model = AgenthailModel(connecting: false)
        model.sessionFilter = .running
        model.adoptRefreshed(listing(ids.map { session($0, "idle") }, seq: 1))
        var changes = 0
        let counting = model.$snapshot.dropFirst().sink { _ in changes += 1 }
        defer { counting.cancel() }
        let inbox = model.catalogFeed.openInbox()
        var seq: UInt64 = 2

        func arriveWhileBusy(_ status: String) {
            let events = ids.enumerated().map { upserted(seq + UInt64($0.offset), $0.element, status) }
            seq += UInt64(events.count)
            let delivered = DispatchSemaphore(value: 0)
            Thread.detachNewThread {
                events.forEach(inbox.append)
                delivered.signal()
            }
            delivered.wait()
        }

        arriveWhileBusy("busy")
        check(spin(until: { working(model) == ids.sorted() }), "every session shows Working after the first burst: \(working(model).count)")
        check(changes == 1, "\(ids.count) events that arrived together took \(changes) snapshot changes, not one")

        for round in 1...8 {
            arriveWhileBusy(round.isMultiple(of: 2) ? "busy" : "idle")
            check(spin(until: { changes == round + 1 }), "burst \(round) applies as one snapshot change: \(changes)")
        }
        arriveWhileBusy("idle")
        check(spin(until: { working(model).isEmpty }), "every session leaves Working after the last burst: \(working(model))")
        check(changes == 10, "10 bursts of \(ids.count) events took \(changes) snapshot changes")
    }

    @MainActor
    static func overflowReloadsInsteadOfApplyingTheBacklog() {
        let model = AgenthailModel(connecting: false)
        model.catalogFeed.bound = 5
        model.adoptRefreshed(listing([session("a", "idle")], seq: 1))
        let inbox = model.catalogFeed.openInbox()
        for seq in 2...10 { inbox.append(upserted(UInt64(seq), "a", "busy", name: "backlog \(seq)")) }
        RunLoop.main.run(until: Date().addingTimeInterval(0.1))
        check(model.knownSessions.first?.name == "a", "an overflowed backlog is not applied: \(model.knownSessions.map(\.name))")
        inbox.append(upserted(11, "a", "busy", name: "late"))
        RunLoop.main.run(until: Date().addingTimeInterval(0.1))
        check(model.knownSessions.first?.name == "a", "an overflowed inbox takes nothing more; the stream resumes from a fresh snapshot")
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
