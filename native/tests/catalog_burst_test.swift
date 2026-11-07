@testable import AgenthailHelpers
import Foundation
import Observation

@MainActor
final class ChangeCounter {
    private(set) var count = 0
    private let read: @MainActor () -> Void

    init(_ read: @escaping @MainActor () -> Void) {
        self.read = read
        arm()
    }

    private func arm() {
        withObservationTracking { read() } onChange: { [weak self] in
            MainActor.assumeIsolated {
                self?.count += 1
                self?.arm()
            }
        }
    }
}

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
        model.knownSessions.filter(\.isWorking).map(\.id).sorted()
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
        counterCountsEveryChange()
        everyScopeChangeReachesItsReaders()
        readerChangesReachTheViewsThatReadThem()
        print("catalog burst tests passed")
    }

    static func removed(_ seq: UInt64, _ id: String) -> CatalogStreamEvent {
        try! JSONDecoder().decode(CatalogStreamEvent.self, from: Data(#"{"stream":"catalog","seq":\#(seq),"type":"session.removed","data":{"sessionId":"\#(id)"}}"#.utf8))
    }

    @MainActor
    static func counterCountsEveryChange() {
        let model = AgenthailModel(connecting: false)
        model.adoptRefreshed(listing([session("a", "idle")], seq: 1))
        let counter = ChangeCounter { _ = model.snapshot }
        model.applyCatalogEvents([upserted(2, "a", "busy")])
        model.applyCatalogEvents([upserted(3, "a", "idle")])
        check(counter.count == 2, "two catalog batches back to back counted \(counter.count) snapshot changes")
    }

    @MainActor
    static func everyScopeChangeReachesItsReaders() {
        let offline = AgenthailAPI(baseURL: URL(string: "http://127.0.0.1:9")!, token: "t", session: URLSession(configuration: .ephemeral))
        var relayed = 0
        let scope = ObservedScope(ScopeReader.Source(api: { offline }, current: { [] }, active: { true }, reachable: { true }, changed: { relayed += 1 }))
        let request = ScopeRequest(scope: "running")
        let changes = ChangeCounter { _ = scope.pages }
        scope.adoptCurrent()
        check(changes.count == 1, "adopting the current rows reached the scope's readers \(changes.count) times")
        scope.refreshed(removedFromCurrent: false)
        check(changes.count == 2, "a refresh reached the scope's readers \(changes.count - 1) times")
        _ = scope.removed("a", unlisted: false)
        check(changes.count == 3, "a removal reached the scope's readers \(changes.count - 2) times")
        scope.drop(request)
        check(changes.count == 4, "dropping a listing reached the scope's readers \(changes.count - 3) times")
        Task { @MainActor in await scope.readMore(request) }
        check(spin(until: { relayed > 0 }), "a failed read reports its change")
        check(changes.count == 5 && scope.pageError != nil, "a failed read reached the scope's readers \(changes.count - 4) times")
    }

    @MainActor
    static func settle() {
        RunLoop.main.run(until: Date().addingTimeInterval(0.05))
    }

    @MainActor
    static func readerChangesReachTheViewsThatReadThem() {
        let offline = AgenthailAPI(baseURL: URL(string: "http://127.0.0.1:9")!, token: "t", session: URLSession(configuration: .ephemeral))
        let model = AgenthailModel(connecting: false, api: offline)
        model.adoptRefreshed(listing([session("a", "idle")], seq: 1))
        settle()

        let current = ChangeCounter { _ = model.listView }
        let view = ScopePage.View(counts: [:], surfaces: [], projects: [], families: [])
        model.adoptCurrentView(ScopeSessions(sessions: [], hostEpoch: "h", catalogSeq: 2, view: view))
        check(current.count >= 1, "a new Current view reaches views that read the listed view: \(current.count)")

        let refreshed = ChangeCounter { _ = model.pages }
        model.adoptRefreshed(listing([session("a", "busy"), session("b", "idle")], seq: 3))
        check(refreshed.count >= 1, "a refreshed snapshot reaches views that read the scope pages: \(refreshed.count)")

        let removal = ChangeCounter { _ = model.pages }
        model.applyCatalogEvents([removed(4, "b")])
        check(removal.count >= 1, "a catalog removal reaches views that read the scope pages: \(removal.count)")

        let failed = ChangeCounter { _ = model.reader.pageError }
        model.sessionFilter = .running
        check(spin(until: { model.reader.pageError != nil }), "a listing read against an unreachable daemon fails")
        check(failed.count >= 1, "a reader change reaches views that read the reader: \(failed.count)")
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
        let counter = ChangeCounter { _ = model.snapshot }
        var changes: Int { counter.count }
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
