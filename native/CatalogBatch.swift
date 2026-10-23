import Foundation

/// Catalog events read from one stream connection wait here until the main
/// actor takes them all at once, so a burst of events costs one snapshot
/// change instead of one per event, and the reader never waits on the main
/// actor. Past its bound the inbox stops taking events and reports an
/// overflow, so its owner reloads from a snapshot instead of applying the
/// backlog.
final class CatalogInbox: @unchecked Sendable {
    enum Contents {
        case events([CatalogStreamEvent])
        case overflow
    }

    private let lock = NSLock()
    private let bound: Int
    private let ready: @Sendable () -> Void
    private var pending: [CatalogStreamEvent] = []
    private var overflowed = false
    private var scheduled = false

    /// ready runs once when events arrive and not again until they are taken,
    /// so the owner schedules at most one drain at a time.
    init(bound: Int = 2000, ready: @escaping @Sendable () -> Void) {
        self.bound = bound
        self.ready = ready
    }

    func append(_ event: CatalogStreamEvent) {
        let schedule: Bool = lock.withLock {
            guard !overflowed else { return false }
            if pending.count >= bound {
                pending.removeAll()
                overflowed = true
            } else {
                pending.append(event)
            }
            defer { scheduled = true }
            return !scheduled
        }
        if schedule { ready() }
    }

    func take() -> Contents {
        lock.withLock {
            scheduled = false
            if overflowed { return .overflow }
            defer { pending.removeAll(keepingCapacity: true) }
            return .events(pending)
        }
    }
}

struct CatalogRemoval: Equatable {
    let id: String
    let unlisted: Bool
    let rekeyedTo: String?
}

struct CatalogBatch {
    var snapshot: DashboardSnapshot
    var cursor: UInt64
    var removals: [CatalogRemoval] = []
    var sessionsChanged = false
    var changed = false
    var needsSnapshot = false
}

extension DashboardSnapshot {
    func applyingCatalog(_ events: [CatalogStreamEvent], after cursor: UInt64) -> CatalogBatch {
        var batch = CatalogBatch(snapshot: self, cursor: cursor)
        for event in events where event.stream == "catalog" && event.seq > batch.cursor {
            batch.cursor = event.seq
            batch.apply(event)
        }
        if batch.sessionsChanged { batch.snapshot.totalSessions = batch.snapshot.sessions.count }
        return batch
    }
}

private extension CatalogBatch {
    mutating func apply(_ event: CatalogStreamEvent) {
        let data = event.data
        switch event.type {
        case "session.upserted":
            guard let changed = data.session else { needsSnapshot = true; return }
            if let index = snapshot.sessions.firstIndex(where: { $0.id == changed.id }) {
                snapshot.sessions[index] = changed
            } else {
                snapshot.sessions.append(changed)
            }
            sessionsChanged = true
        case "session.queue":
            guard let id = data.sessionId, let count = data.queueCount,
                  let index = snapshot.sessions.firstIndex(where: { $0.id == id }) else { return }
            snapshot.sessions[index].queueCount = count
            sessionsChanged = true
        case "session.removed":
            guard let id = data.sessionId else { needsSnapshot = true; return }
            snapshot.sessions.removeAll { $0.id == id }
            removals.append(CatalogRemoval(id: id, unlisted: data.reason == "unlisted", rekeyedTo: data.rekeyedTo))
            sessionsChanged = true
        case "delivery.problem":
            guard let deliveryID = data.deliveryId, let sessionID = data.sessionId else { return }
            let problem = DeliveryProblem(deliveryId: deliveryID, sessionId: sessionID, sourceSessionId: data.sourceSessionId, attentionSessionId: data.attentionSessionId, message: data.message ?? "", reason: data.reason ?? "", status: data.status, at: data.at ?? "")
            var problems = snapshot.deliveryProblems ?? []
            problems.removeAll { $0.deliveryId == deliveryID }
            problems.insert(problem, at: 0)
            snapshot.deliveryProblems = Array(problems.prefix(50))
        case "delivery.dismissed":
            guard let deliveryID = data.deliveryId else { return }
            snapshot.deliveryProblems?.removeAll { $0.deliveryId == deliveryID }
        case "surface.health":
            guard let name = data.surface, let health = data.health,
                  let index = snapshot.surfaces.firstIndex(where: { $0.name == name }) else { needsSnapshot = true; return }
            let previous = snapshot.surfaces[index]
            // The daemon derives the repair from the runtime problem, and health events carry no repair.
            let repairs = data.runtime?.problem == previous.runtime?.problem
            snapshot.surfaces[index] = SurfaceState(name: name, connected: health == "healthy", error: health == "healthy" ? nil : data.detail, health: health, healthDetail: data.detail, capabilities: previous.capabilities, runtime: data.runtime, repairAction: repairs ? previous.repairAction : nil, repairLabel: repairs ? previous.repairLabel : nil)
        default:
            return
        }
        changed = true
    }
}
