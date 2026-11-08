import Foundation

/// Delivers each session's messages in order, one at a time, until each has an answer.
@MainActor
final class SendQueue {
    static let retryWindow: TimeInterval = 60 * 60

    private(set) var pending: [String: [PendingSend]] = [:]
    var now: () -> Date = { Date() }
    var deliver: ((PendingSend) async -> SendOutcome)?
    var changed: (() -> Void)?
    var refused: ((PendingSend, String) -> Void)?
    var unconfirmed: ((PendingSend) -> Void)?
    private var workers: [String: Task<Void, Never>] = [:]
    private var waits: [String: Task<Void, Never>] = [:]
    private var moves: [String: String] = [:]
    private var delivering: Set<UUID> = []

    deinit {
        workers.values.forEach { $0.cancel() }
        waits.values.forEach { $0.cancel() }
    }

    func add(_ send: PendingSend) {
        pending[send.sessionID, default: []].append(send)
        changed?()
        start(send.sessionID)
    }

    /// Saved messages are older than anything queued this launch, so they go first.
    func restore(_ sends: [PendingSend]) {
        guard !sends.isEmpty else { return }
        for (session, saved) in Dictionary(grouping: sends, by: \.sessionID) {
            pending[session] = saved + (pending[session] ?? [])
        }
        changed?()
        for session in Set(sends.map(\.sessionID)) { start(session) }
    }

    func wake() {
        waits.values.forEach { $0.cancel() }
    }

    func retry(_ send: PendingSend) {
        update(send) {
            $0.retryStopped = false
            $0.retryingOnTap = true
        }
        changed?()
        wake()
        if let session = pending.first(where: { $0.value.contains { $0.id == send.id } })?.key { start(session) }
    }

    func setUploaded(_ uploaded: [UploadedAttachment], for send: PendingSend) {
        update(send) {
            $0.uploaded = uploaded.map(\.id)
            $0.files = uploaded
        }
        changed?()
    }

    func setWire(_ wire: ActionWire, for send: PendingSend) {
        update(send) { $0.wire = wire }
        changed?()
    }

    func holds(_ send: PendingSend) -> Bool {
        pending.values.contains { $0.contains { $0.id == send.id } }
    }

    func remove(_ send: PendingSend) {
        for (session, sends) in pending where sends.contains(where: { $0.id == send.id }) {
            let remaining = sends.filter { $0.id != send.id }
            pending[session] = remaining.isEmpty ? nil : remaining
        }
        changed?()
    }

    func removeAll(where owned: (String) -> Bool) {
        pending = pending.filter { !owned($0.key) }
        changed?()
    }

    /// A session that continues under a new id keeps its messages and the worker delivering them.
    func carry(_ rekeys: SessionRekeys) {
        var moved = false
        for session in Array(pending.keys) where rekeys.resolve(session) != session {
            let target = rekeys.resolve(session)
            let sends = (pending.removeValue(forKey: session) ?? []).map { send in
                var send = send
                send.sessionID = target
                return send
            }
            pending[target, default: []].append(contentsOf: sends)
            moved = true
            let worker = workers.removeValue(forKey: session)
            if let worker, workers[target] == nil {
                workers[target] = worker
                moves[session] = target
                waits[target] = waits.removeValue(forKey: session)
            } else {
                worker?.cancel()
                waits.removeValue(forKey: session)?.cancel()
                start(target)
            }
        }
        if moved { changed?() }
    }

    private func update(_ send: PendingSend, _ change: (inout PendingSend) -> Void) {
        for (session, sends) in pending {
            if let index = sends.firstIndex(where: { $0.id == send.id }) {
                change(&pending[session]![index])
            }
        }
    }

    private func current(_ session: String) -> String {
        var session = session
        while let moved = moves[session] { session = moved }
        return session
    }

    private func start(_ sessionID: String) {
        guard workers[sessionID] == nil else { return }
        let backoff = EventRetryBackoff()
        workers[sessionID] = Task { [weak self] in
            while await self?.deliverNext(sessionID, backoff: backoff) == true {}
            self?.finish(sessionID)
        }
    }

    private func finish(_ sessionID: String) {
        let session = current(sessionID)
        workers[session] = nil
        moves = moves.filter { $0.value != session }
        changed?()
    }

    private func stopRetrying(_ send: PendingSend) {
        update(send) {
            $0.unconfirmed = true
            $0.retryStopped = true
            $0.retryingOnTap = false
        }
        changed?()
        if let stopped = pending.values.joined().first(where: { $0.id == send.id }) { unconfirmed?(stopped) }
    }

    private func deliverNext(_ session: String, backoff: EventRetryBackoff) async -> Bool {
        for send in pending[current(session)] ?? [] where !send.retryStopped && !send.retryingOnTap && now().timeIntervalSince(send.sentAt) >= Self.retryWindow {
            stopRetrying(send)
        }
        guard let send = pending[current(session)]?.first(where: { !$0.retryStopped }), let deliver else { return false }
        guard !delivering.contains(send.id) else {
            try? await Task.sleep(for: .milliseconds(50))
            return !Task.isCancelled
        }
        delivering.insert(send.id)
        let outcome = await deliver(send)
        delivering.remove(send.id)
        guard !Task.isCancelled else { return false }
        await settle(send, outcome, backoff: backoff, session: session)
        return true
    }

    private func settle(_ send: PendingSend, _ outcome: SendOutcome, backoff: EventRetryBackoff, session: String) async {
        guard holds(send) else { return }
        switch outcome {
        case .answered:
            backoff.reset()
            remove(send)
        case .refused(let reason):
            backoff.reset()
            remove(send)
            refused?(send, reason)
        case .unconfirmed:
            if send.retryingOnTap {
                stopRetrying(send)
                return
            }
            update(send) { $0.unconfirmed = true }
            changed?()
            unconfirmed?(send)
            let delay = backoff.nextDelay()
            let wait = Task { _ = try? await Task.sleep(for: .seconds(delay)) }
            waits[current(session)] = wait
            await wait.value
            waits[current(session)] = nil
        }
    }
}
