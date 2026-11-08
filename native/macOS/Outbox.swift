import Foundation
import Observation

@MainActor
@Observable
final class Outbox {
    private(set) var rows: [String: [LocalSend]] = [:]
    @ObservationIgnored private let queue = SendQueue()
    @ObservationIgnored var api: () -> AgenthailAPI? = { nil }
    @ObservationIgnored var resolve: (String) -> String = { $0 }
    @ObservationIgnored var returnToComposer: (LocalSend, String) -> Void = { _, _ in }
    @ObservationIgnored var changed: () -> Void = {}
    @ObservationIgnored var notice: (String?) -> Void = { _ in }
    @ObservationIgnored private var completions: [UUID: (Bool) -> Void] = [:]
    @ObservationIgnored private var kept: [UUID: KeptDelivery] = [:]

    var saved: [ComposerStore.Saved.Send] {
        queue.pending.values.joined().map(ComposerStore.Saved.Send.init)
    }

    init() {
        queue.deliver = { [weak self] send in await self?.deliver(send) ?? .unconfirmed }
        queue.changed = { [weak self] in self?.follow() }
        queue.refused = { [weak self] send, reason in self?.refuse(send, reason) }
        queue.unconfirmed = { [weak self] send in self?.finish(send.id, delivered: false) }
    }

    func send(_ text: String, attachments: [ComposerAttachment], to sessionID: String, busyDelivery: String?, turnOptions: MessageTurnOptions? = nil, completion: ((Bool) -> Void)?) {
        let pending = PendingSend(sessionID: sessionID, message: text, attachments: attachments, busyDelivery: busyDelivery, turnOptions: turnOptions)
        rows[sessionID, default: []].append(LocalSend(id: pending.id, text: text, attachments: attachments, sentAt: pending.sentAt, status: nil, busyDelivery: busyDelivery, turnOptions: pending.turnOptions))
        if let completion { completions[pending.id] = completion }
        queue.add(pending)
    }

    func record(_ row: LocalSend, in sessionID: String) {
        rows[resolve(sessionID), default: []].append(row)
    }

    func retry(_ row: LocalSend, in sessionID: String) {
        let session = resolve(sessionID)
        if let pending = held(row, in: session) {
            queue.retry(pending)
            return
        }
        guard row.failure != nil else { return }
        // The daemon keeps a refusal under its key, so the retry is a new request.
        var pending = PendingSend(id: row.id, sessionID: session, message: row.text, attachments: row.attachments, uploaded: row.uploaded?.map(\.id), busyDelivery: row.busyDelivery, turnOptions: row.turnOptions)
        pending.files = row.uploaded
        update(row.id, in: session) {
            $0.failure = nil
            $0.deliveryID = nil
            $0.deletable = false
        }
        queue.add(pending)
    }

    func putBack(_ row: LocalSend, in sessionID: String) {
        let session = resolve(sessionID)
        guard let pending = held(row, in: session) else { return }
        queue.remove(pending)
        finish(row.id, delivered: false)
        drop(row.id, in: session)
        returnToComposer(row, session)
    }

    func discard(_ row: LocalSend, in sessionID: String) {
        let session = resolve(sessionID)
        if let pending = held(row, in: session) { queue.remove(pending) }
        finish(row.id, delivered: false)
        drop(row.id, in: session)
    }

    func delete(_ row: LocalSend, in sessionID: String) {
        guard row.canDelete else { return }
        let session = resolve(sessionID)
        let position = rows[session]?.firstIndex { $0.id == row.id }
        discard(row, in: session)
        guard let deliveryID = row.deliveryID, let api = api() else { return }
        Task {
            do {
                try await api.action("delivery-delete", deliveryID: deliveryID)
                notice(nil)
            } catch {
                notice(AgenthailAPIError.userMessage(error))
                let session = resolve(session)
                if let position, rows[session]?.contains(where: { $0.id == row.id }) != true {
                    rows[session, default: []].insert(row, at: min(position, rows[session]?.count ?? 0))
                }
            }
        }
    }

    func reconcile(_ items: [TimelineItem], for sessionID: String) {
        guard let sends = rows[sessionID] else { return }
        rows[sessionID] = LocalSend.reconcile(sends, with: items)
    }

    func carry(_ rekeys: SessionRekeys) {
        queue.carry(rekeys)
        for session in Array(rows.keys) where resolve(session) != session {
            let target = resolve(session)
            rows[target] = (rows[target] ?? []) + (rows.removeValue(forKey: session) ?? [])
        }
    }

    func wake() {
        queue.wake()
    }

    func restore(_ saved: [ComposerStore.Saved.Send]) -> [ComposerStore.Saved.Send] {
        let resumable = saved.compactMap(\.resumable)
        for (session, sends) in Dictionary(grouping: resumable, by: \.sessionID) {
            let restored = sends.map { LocalSend(id: $0.id, text: $0.message, attachments: $0.attachments, uploaded: $0.files, sentAt: $0.sentAt, status: nil, busyDelivery: $0.busyDelivery, unconfirmed: true, turnOptions: $0.turnOptions) }
            rows[session] = restored + (rows[session] ?? [])
        }
        queue.restore(resumable)
        return saved.filter { $0.resumable == nil }
    }

    private func deliver(_ send: PendingSend) async -> SendOutcome {
        kept[send.id] = nil
        guard let api = api() else { return .unconfirmed }
        do {
            var uploaded = send.uploaded
            if uploaded == nil {
                let stored = try await api.upload(send.attachments, sessionID: resolve(send.sessionID))
                queue.setUploaded(stored, for: send)
                update(send.id, in: send.sessionID) { $0.uploaded = stored }
                uploaded = stored.map(\.id)
            }
            let wire = try send.wire ?? api.instructionWire(sessionID: resolve(send.sessionID), message: send.message, busyDelivery: send.busyDelivery, author: "person", attachments: uploaded ?? [], turnOptions: send.turnOptions, idempotencyKey: send.idempotencyKey)
            if send.wire == nil { queue.setWire(wire, for: send) }
            let receipt = try await api.send(wire)
            update(send.id, in: send.sessionID) {
                $0.status = LocalSend.label(for: receipt.result)
                $0.unconfirmed = false
                $0.retryStopped = false
            }
            finish(send.id, delivered: true)
            return .answered
        } catch {
            if let delivery = error as? KeptDelivery { kept[send.id] = delivery }
            return .of(error)
        }
    }

    private func refuse(_ send: PendingSend, _ reason: String) {
        let delivery = kept.removeValue(forKey: send.id)
        update(send.id, in: send.sessionID) {
            $0.failure = reason
            $0.deliveryID = delivery?.deliveryID
            $0.deletable = delivery?.deletable ?? false
            $0.unconfirmed = false
            $0.retryStopped = false
        }
        finish(send.id, delivered: false)
    }

    private func follow() {
        for (session, sends) in queue.pending {
            for send in sends {
                update(send.id, in: session) {
                    $0.unconfirmed = send.unconfirmed
                    $0.retryStopped = send.retryStopped
                }
            }
        }
        changed()
    }

    private func finish(_ id: UUID, delivered: Bool) {
        completions.removeValue(forKey: id)?(delivered)
    }

    private func held(_ row: LocalSend, in session: String) -> PendingSend? {
        queue.pending[session]?.first { $0.id == row.id }
    }

    private func drop(_ id: UUID, in session: String) {
        rows[session]?.removeAll { $0.id == id }
        if rows[session]?.isEmpty == true { rows[session] = nil }
    }

    private func update(_ id: UUID, in sessionID: String, _ change: (inout LocalSend) -> Void) {
        let session = resolve(sessionID)
        guard let index = rows[session]?.firstIndex(where: { $0.id == id }) else { return }
        var row = rows[session]![index]
        change(&row)
        if row != rows[session]![index] { rows[session]![index] = row }
    }
}
