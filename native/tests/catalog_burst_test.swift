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
    static func spin(until done: () -> Bool, within seconds: TimeInterval) -> Bool {
        let deadline = Date().addingTimeInterval(seconds)
        while !done() {
            if Date() > deadline { return false }
            RunLoop.main.run(until: Date().addingTimeInterval(0.005))
        }
        return true
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
        check(spin(until: { working(model).isEmpty }, within: 2), "the pending idle after that snapshot still applies: \(working(model))")

        model.adoptRefreshed(listing([session("a", "busy")], seq: 11))
        check(working(model).isEmpty, "a snapshot read before an applied idle keeps the session idle")

        inbox.append(upserted(13, "a", "busy", name: "skipped"))
        inbox.append(upserted(14, "a", "busy", name: "skipped"))
        model.adoptRefreshed(listing([session("a", "busy", name: "from snapshot")], seq: 14))
        inbox.append(upserted(15, "a", "idle", name: "after snapshot"))
        check(spin(until: { model.knownSessions.first?.name == "after snapshot" }, within: 2), "events after a snapshot apply: \(model.knownSessions.map(\.name))")
        check(working(model).isEmpty, "the last event wins over the snapshot it followed")
    }

    final class Emitted: @unchecked Sendable {
        private let lock = NSLock()
        private var last: Date?
        var at: Date? { lock.withLock { last } }
        func mark() { lock.withLock { last = Date() } }
    }

    @MainActor
    static func burstKeepsTheSidebarCurrent() {
        let ids = (0..<30).map { "s\($0)" }
        let model = AgenthailModel(connecting: false)
        model.sessionFilter = .running
        model.adoptRefreshed(listing(ids.map { session($0, "idle") }, seq: 1))
        var renders = 0
        let render = model.$snapshot.dropFirst().sink { _ in
            renders += 1
            Thread.sleep(forTimeInterval: 0.05)
        }
        defer { render.cancel() }
        let inbox = model.catalogFeed.openInbox()

        func burst(_ events: [(String, String)], from seq: UInt64) -> Emitted {
            let emitted = Emitted()
            Thread.detachNewThread {
                for (offset, (id, status)) in events.enumerated() {
                    inbox.append(upserted(seq + UInt64(offset), id, status))
                    emitted.mark()
                    Thread.sleep(forTimeInterval: 1.0 / 30)
                }
            }
            return emitted
        }

        let started = burst(ids.map { ($0, "busy") }, from: 2)
        check(spin(until: { working(model) == ids.sorted() }, within: 5), "every session shows Working after the first burst: \(working(model).count)")
        if let last = started.at { check(Date().timeIntervalSince(last) < 1, "the sidebar showed Working within a second of the last event") }

        renders = 0
        var toggles: [(String, String)] = []
        for round in 0..<8 { toggles += ids.map { ($0, round.isMultiple(of: 2) ? "idle" : "busy") } }
        toggles += ids.map { ($0, "idle") }
        let finished = burst(toggles, from: 32)
        check(spin(until: { working(model).isEmpty && finished.at != nil }, within: 20), "every session leaves Working after the burst: \(working(model))")
        let lag = Date().timeIntervalSince(finished.at ?? .distantPast)
        check(lag < 1, "the sidebar showed idle \(lag) s after the last of \(toggles.count) events")
        check(renders < toggles.count / 3, "\(toggles.count) events took \(renders) snapshot changes, not one each")
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
