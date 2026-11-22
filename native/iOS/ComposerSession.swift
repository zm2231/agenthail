import Combine
import Foundation
import os
import UIKit

@MainActor
protocol ComposerRouting: AnyObject {
    var selectedSessionID: String? { get }
    var selectedDetail: SessionDetail? { get }
    var deliveryStatus: [String: String] { get set }
    var deliveryIDs: [String: Int64] { get set }
    var deliveryQueueIDs: [String: Int64] { get set }
    var operationError: String? { get set }
    var rekeys: SessionRekeys { get }
    func resolve(_ sessionID: String) throws -> (api: AgenthailAPI, route: SessionRoute)
    func deliveryLabel(_ receipt: DeliveryReceipt?) -> String
    func refreshSession(_ id: String) async
}

@MainActor
@Observable
final class ComposerSession {
    let draft = ComposerDraft()
    var composer: String {
        get { draft.text }
        set { draft.text = newValue }
    }
    var sendingSessionIDs: Set<String> = []
    private static let composerLog = Logger(subsystem: "com.agenthail.ios", category: "Composer")
    private var drafts: [String: String] = [:] {
        didSet { saveComposerStateSoon() }
    }
    private(set) var stashes: [String: ComposerStash] = [:] {
        didSet { saveComposerStateSoon() }
    }
    private let sendQueue = SendQueue()
    private(set) var pendingSends: [String: [PendingSend]] = [:]
    var failedSends: [String: String] = [:]
    private(set) var refusedSends: [String: RefusedSend] = [:]
    @ObservationIgnored private var keptDeliveries: [String: KeptDelivery] = [:]
    private var lostAttachments: [String: [String]] = [:]
    @ObservationIgnored private var composerStore: ComposerStore?
    @ObservationIgnored private var waitingComposerStore: ComposerStore?
    @ObservationIgnored private var protectedDataObserver: NSObjectProtocol?
    @ObservationIgnored nonisolated(unsafe) private var answerWatch: NSObjectProtocol?
    private static let composerFileKeptNotice = "Saved drafts couldn’t be loaded. Their file was kept on this iPhone."
    private static let composerUnreadNotice = "Saved drafts couldn’t be read. Until they can, drafts and unconfirmed messages are lost if Agenthail closes."
    @ObservationIgnored private var composerWaitAnnounced = false
    @ObservationIgnored private var composerSave: Task<Void, Never>?
    @ObservationIgnored private var spoolTried: Set<UUID> = []
    @ObservationIgnored private var draftChanges: AnyCancellable?
    var composerAttachments: [ComposerAttachment] = [] {
        didSet { saveComposerStateSoon() }
    }
    var attachmentProblem: String?
    private var attachmentDrafts: [String: [ComposerAttachment]] = [:] {
        didSet { saveComposerStateSoon() }
    }
    @ObservationIgnored private weak var router: ComposerRouting?

    init(router: ComposerRouting) {
        self.router = router
    }

    deinit {
        if let answerWatch { NotificationCenter.default.removeObserver(answerWatch) }
    }

    private var selectedSessionID: String? { router?.selectedSessionID }
    private var selectedDetail: SessionDetail? { router?.selectedDetail }
    private var deliveryStatus: [String: String] {
        get { router?.deliveryStatus ?? [:] }
        set { router?.deliveryStatus = newValue }
    }
    private var deliveryIDs: [String: Int64] {
        get { router?.deliveryIDs ?? [:] }
        set { router?.deliveryIDs = newValue }
    }
    private var deliveryQueueIDs: [String: Int64] {
        get { router?.deliveryQueueIDs ?? [:] }
        set { router?.deliveryQueueIDs = newValue }
    }
    private var operationError: String? {
        get { router?.operationError }
        set { router?.operationError = newValue }
    }
    private var rekeys: SessionRekeys { router?.rekeys ?? SessionRekeys() }

    private func resolve(_ sessionID: String) throws -> (api: AgenthailAPI, route: SessionRoute) {
        guard let router else { throw CancellationError() }
        return try router.resolve(sessionID)
    }

    private func deliveryLabel(_ receipt: DeliveryReceipt?) -> String { router?.deliveryLabel(receipt) ?? "" }

    private func refreshSession(_ id: String) async { await router?.refreshSession(id) }

    func start(store: ComposerStore?) {
        composerStore = store
        connectSendQueue()
        watchRequestAnswers()
        restoreComposerState()
    }

    func hasDraft(_ id: String) -> Bool { drafts[id]?.isEmpty == false }

    func setDraft(_ text: String, for id: String) { drafts[id] = text }

    func wake() { sendQueue.wake() }

    func leave(_ id: String) {
        drafts[id] = composer
        attachmentDrafts[id] = composerAttachments
    }

    func enter(_ id: String) {
        composer = drafts[id] ?? ""
        composerAttachments = attachmentDrafts[id] ?? []
        attachmentProblem = nil
        announceLostAttachments(for: id)
    }

    func carry(_ rekeys: SessionRekeys) {
        _ = rekeys.carry(&drafts)
        _ = rekeys.carry(&attachmentDrafts)
        var held = stashes
        if rekeys.carry(&held) { stashes = held }
        sendQueue.carry(rekeys)
        var failures = failedSends
        if rekeys.carry(&failures) { failedSends = failures }
        var refused = refusedSends
        if rekeys.carry(&refused) { refusedSends = refused }
        _ = rekeys.carry(&lostAttachments)
    }

    func clearComposer() {
        composer = ""
        composerAttachments = []
    }

    func forget(where owned: (String) -> Bool) {
        drafts = drafts.filter { !owned($0.key) }
        attachmentDrafts = attachmentDrafts.filter { !owned($0.key) }
        sendQueue.removeAll(where: owned)
        failedSends = failedSends.filter { !owned($0.key) }
        refusedSends = refusedSends.filter { !owned($0.key) }
        lostAttachments = lostAttachments.filter { !owned($0.key) }
        stashes = stashes.filter { !owned($0.key) }
    }

    func returnToDraft(_ message: String, attachments: [QueueAttachment], id: String) {
        if selectedSessionID == id {
            composer = QueueTakeBack.text(queued: [message], draft: composer)
            let taken = ComposerAttachments.takingBack(attachments, to: composerAttachments)
            composerAttachments = taken.attachments
            attachmentProblem = ComposerAttachments.lostNotice(taken.lost) ?? attachmentProblem
        } else {
            drafts[id] = QueueTakeBack.text(queued: [message], draft: drafts[id] ?? "")
            let taken = ComposerAttachments.takingBack(attachments, to: attachmentDrafts[id] ?? [])
            attachmentDrafts[id] = taken.attachments
            operationError = ComposerAttachments.lostNotice(taken.lost) ?? operationError
        }
    }

    /// Stashes the open session's draft, or brings its stash back into an
    /// empty composer; stashing while a stash is held swaps the two.
    func toggleStash() {
        guard let id = selectedSessionID else { return }
        let empty = ComposerAttachments.isEmpty(text: composer, attachments: composerAttachments)
        if !empty {
            let current = ComposerStash(text: composer, attachments: composerAttachments)
            composer = stashes[id]?.text ?? ""
            composerAttachments = stashes[id]?.attachments ?? []
            stashes[id] = current
        } else if let held = stashes.removeValue(forKey: id) {
            composer = held.text
            composerAttachments = held.attachments
        }
    }

    func addAttachments(_ new: [ComposerAttachment], problem: ComposerAttachmentProblem? = nil) {
        let added = ComposerAttachments.adding(new, to: composerAttachments)
        composerAttachments = added.attachments
        attachmentProblem = (problem ?? added.problem)?.message
    }

    func paste(_ pasted: PastedAttachments) {
        addAttachments(pasted.attachments, problem: pasted.problem)
    }

    func removeAttachment(_ id: UUID) {
        composerAttachments.removeAll { $0.id == id }
        attachmentProblem = nil
    }

    func editAndResend(_ text: String) {
        let edited = text.trimmingCharacters(in: .whitespacesAndNewlines)
        composer = composer.isEmpty ? edited : edited + "\n\n" + composer
    }

    func send(to session: SessionState) {
        let message = PastedTextStore.shared.expand(composer).trimmingCharacters(in: .whitespacesAndNewlines)
        let attachments = composerAttachments
        guard !ComposerAttachments.isEmpty(text: message, attachments: attachments) else { return }
        let kept = RefusedSend(message: message, attachments: attachments)
        guard let detail = selectedDetail, detail.session.id == session.id else {
            failedSends[session.id] = "This session is still opening. Your draft is kept."
            refusedSends[session.id] = kept
            return
        }
        guard !detail.readOnly, detail.capabilities.send else {
            failedSends[session.id] = "This session can't take messages. Your draft is kept."
            refusedSends[session.id] = kept
            return
        }
        do { _ = try self.resolve(session.id) } catch {
            failedSends[session.id] = AgenthailAPIError.userMessage(error)
            refusedSends[session.id] = kept
            return
        }
        failedSends[session.id] = nil
        refusedSends[session.id] = nil
        deliveryQueueIDs.removeValue(forKey: session.id)
        deliveryStatus[session.id] = "Sending…"
        composer = ""
        drafts[session.id] = ""
        composerAttachments = []
        attachmentDrafts[session.id] = []
        attachmentProblem = nil
        if let held = stashes.removeValue(forKey: session.id) {
            if selectedSessionID == session.id {
                composer = held.text
                composerAttachments = held.attachments
            } else {
                drafts[session.id] = held.text
                attachmentDrafts[session.id] = held.attachments
            }
        }
        sendQueue.add(PendingSend(sessionID: session.id, message: message, attachments: attachments))
    }

    private func deliver(_ send: PendingSend) async -> SendOutcome {
        keptDeliveries[send.idempotencyKey] = nil
        var background = UIBackgroundTaskIdentifier.invalid
        background = UIApplication.shared.beginBackgroundTask(withName: "send") {
            UIApplication.shared.endBackgroundTask(background)
            background = .invalid
        }
        defer {
            if background != .invalid { UIApplication.shared.endBackgroundTask(background) }
        }
        do {
            let (api, route) = try resolve(send.sessionID)
            var uploaded = send.uploaded
            if uploaded == nil {
                await keepAttachments(of: send)
                let stored = try await sendQueue.upload(send) { try await api.upload($0, sessionID: route.sessionID) }
                uploaded = stored.map(\.id)
            }
            let wire = try send.wire ?? api.instructionWire(sessionID: route.sessionID, message: send.message, author: "person", attachments: uploaded ?? [], idempotencyKey: send.idempotencyKey)
            if send.wire == nil { sendQueue.setWire(wire, for: send) }
            let response = try await api.send(wire)
            if sendQueue.holds(send) {
                if let deliveryID = response.result?.deliveryId { deliveryIDs[send.sessionID] = deliveryID }
                deliveryStatus[send.sessionID] = deliveryLabel(response.result)
                if response.result?.evidence == "queued", let queueID = response.result?.queueId {
                    deliveryQueueIDs[send.sessionID] = queueID
                }
            }
            await refreshSession(send.sessionID)
            return .answered
        } catch {
            if let kept = error as? KeptDelivery { keptDeliveries[send.idempotencyKey] = kept }
            return .of(error)
        }
    }

    private func returnToComposer(_ send: PendingSend) {
        if selectedSessionID == send.sessionID {
            composer = composer.isEmpty ? send.message : send.message + "\n\n" + composer
            composerAttachments = ComposerAttachments.adding(composerAttachments, to: send.attachments).attachments
        } else {
            let newer = drafts[send.sessionID] ?? ""
            drafts[send.sessionID] = newer.isEmpty ? send.message : send.message + "\n\n" + newer
            attachmentDrafts[send.sessionID] = ComposerAttachments.adding(attachmentDrafts[send.sessionID] ?? [], to: send.attachments).attachments
        }
    }

    private func sendWasRefused(_ send: PendingSend, reason: String) {
        deliveryIDs.removeValue(forKey: send.sessionID)
        deliveryStatus[send.sessionID] = "Couldn’t complete request. Draft kept."
        failedSends[send.sessionID] = reason
        let kept = keptDeliveries.removeValue(forKey: send.idempotencyKey)
        let refused = RefusedSend(message: send.message, attachments: send.attachments, ask: kept.map { FailedSendAsk(id: $0.askID) })
        refusedSends[send.sessionID] = refused
        if selectedSessionID != send.sessionID { operationError = reason }
        returnToComposer(send)
        saveComposerState()
        readRefusedAsk(refused, in: send.sessionID)
    }

    func deleteRefusedSend(in sessionID: String) {
        guard let refused = refusedSends[sessionID], refused.canDelete else { return }
        guard let ask = refused.ask else {
            dropRefusedSend(refused, in: sessionID)
            return
        }
        Task {
            do {
                let (api, route) = try resolve(sessionID)
                _ = try await api.respond(sessionID: route.sessionID, requestID: ask.id, decision: .choose(FailedSendAsk.delete))
                dropRefusedSend(refused, in: rekeys.resolve(sessionID))
            } catch {
                switch SettledAsk.of(error) {
                case .gone:
                    dropRefusedSend(refused, in: rekeys.resolve(sessionID))
                case .resent:
                    operationError = SettledAsk.resentNotice
                    dropRefusedSend(refused, in: rekeys.resolve(sessionID))
                case nil:
                    operationError = AgenthailAPIError.userMessage(error)
                    readRefusedAsk(refused, in: rekeys.resolve(sessionID))
                }
            }
        }
    }

    private func readRefusedAsk(_ refused: RefusedSend, in sessionID: String) {
        guard let ask = refused.ask else { return }
        Task {
            var requests: SessionRequests?
            if let (api, route) = try? resolve(sessionID) { requests = try? await api.requests(sessionID: route.sessionID) }
            let session = rekeys.resolve(sessionID)
            guard let current = refusedSends[session], current.ask?.id == ask.id else { return }
            refusedSends[session]?.ask = requests.map(ask.reading) ?? FailedSendAsk(id: ask.id)
        }
    }

    func refusedSendDeleted(ask askID: String, choice: String?) {
        guard choice == FailedSendAsk.delete else { return }
        for (session, refused) in refusedSends where refused.ask?.id == askID {
            dropRefusedSend(refused, in: session)
        }
    }

    private func watchRequestAnswers() {
        answerWatch = NotificationCenter.default.addObserver(forName: .agenthailRequestAnswered, object: nil, queue: .main) { [weak self] note in
            guard let answered = note.object as? AnsweredRequest else { return }
            MainActor.assumeIsolated { self?.refusedSendDeleted(ask: answered.requestID, choice: answered.choice) }
        }
    }

    private func dropRefusedSend(_ refused: RefusedSend, in sessionID: String) {
        guard refusedSends[sessionID] == refused else { return }
        refusedSends[sessionID] = nil
        failedSends[sessionID] = nil
        deliveryStatus[sessionID] = nil
        if selectedSessionID == sessionID {
            let left = refused.removed(from: composer, attachments: composerAttachments)
            composer = left.text
            composerAttachments = left.attachments
        } else {
            let left = refused.removed(from: drafts[sessionID] ?? "", attachments: attachmentDrafts[sessionID] ?? [])
            drafts[sessionID] = left.text
            attachmentDrafts[sessionID] = left.attachments
        }
        saveComposerState()
    }

    func putBack(_ send: PendingSend) {
        guard sendQueue.holds(send) else { return }
        sendQueue.remove(send)
        deliveryIDs.removeValue(forKey: send.sessionID)
        deliveryStatus[send.sessionID] = "Put back. It may already have arrived."
        returnToComposer(send)
        saveComposerState()
    }

    func retrySend(_ send: PendingSend) {
        guard sendQueue.holds(send) else { return }
        deliveryStatus[send.sessionID] = "Not confirmed yet · retrying"
        sendQueue.retry(send)
    }

    private func connectSendQueue() {
        sendQueue.deliver = { [weak self] send in await self?.deliver(send) ?? .unconfirmed }
        sendQueue.changed = { [weak self] in
            guard let self else { return }
            pendingSends = sendQueue.pending
            sendingSessionIDs = Set(sendQueue.pending.keys)
            saveComposerState()
        }
        sendQueue.refused = { [weak self] send, reason in self?.sendWasRefused(send, reason: reason) }
        sendQueue.unconfirmed = { [weak self] send in self?.deliveryStatus[send.sessionID] = send.unconfirmedTitle }
    }

    private func restoreComposerState() {
        guard let composerStore else { return }
        openComposerStore(composerStore)
        draftChanges = draft.$text.dropFirst().sink { [weak self] _ in self?.saveComposerStateSoon() }
    }

    private func openComposerStore(_ store: ComposerStore) {
        let loaded = store.load()
        if loaded.unreadable, loaded.setAside == nil {
            Self.composerLog.error("composer file could not be read; left in place, not saving until it can be")
            composerStore = nil
            waitForComposerStore(store, announce: !loaded.locked)
            return
        }
        if loaded.unreadable {
            Self.composerLog.error("composer file unreadable; set aside as \(loaded.setAside?.lastPathComponent ?? "", privacy: .public)")
            operationError = Self.composerFileKeptNotice
        }
        let waited = waitingComposerStore != nil
        stopWaitingForComposerStore()
        composerStore = store
        mergeSavedComposerState(loaded.saved)
        if waited { saveComposerState() }
    }

    private func waitForComposerStore(_ store: ComposerStore, announce: Bool) {
        if announce, !composerWaitAnnounced {
            composerWaitAnnounced = true
            operationError = Self.composerUnreadNotice
        }
        waitingComposerStore = store
        guard protectedDataObserver == nil else { return }
        protectedDataObserver = NotificationCenter.default.addObserver(forName: UIApplication.protectedDataDidBecomeAvailableNotification, object: nil, queue: nil) { [weak self] _ in
            MainActor.assumeIsolated { self?.retryComposerStore() }
        }
    }

    private func stopWaitingForComposerStore() {
        waitingComposerStore = nil
        if let protectedDataObserver { NotificationCenter.default.removeObserver(protectedDataObserver) }
        protectedDataObserver = nil
        if operationError == Self.composerUnreadNotice { operationError = nil }
    }

    func retryComposerStore() {
        guard let waitingComposerStore else { return }
        openComposerStore(waitingComposerStore)
    }

    private func mergeSavedComposerState(_ saved: ComposerStore.Saved) {
        func joined(_ earlier: String, _ later: String) -> String {
            if later.isEmpty || later == earlier { return earlier }
            return earlier.isEmpty ? later : earlier + "\n\n" + later
        }
        func addToDraft(_ text: String, for session: String) {
            if session == selectedSessionID {
                composer = joined(text, composer)
            } else {
                drafts[session] = joined(text, drafts[session] ?? "")
            }
        }
        for (session, text) in saved.drafts { addToDraft(text, for: session) }
        for (session, text) in saved.stashes {
            if stashes[session] == nil {
                stashes[session] = ComposerStash(text: text, attachments: [])
            } else {
                addToDraft(text, for: session)
            }
        }
        func attach(_ restored: [ComposerAttachment], to session: String) {
            if session == selectedSessionID {
                composerAttachments = ComposerAttachments.adding(restored, to: composerAttachments).attachments
            } else {
                attachmentDrafts[session] = ComposerAttachments.adding(restored, to: attachmentDrafts[session] ?? []).attachments
            }
        }
        func addToStash(_ restored: [ComposerAttachment], for session: String) {
            if var stash = stashes[session] {
                stash.attachments = ComposerAttachments.adding(restored, to: stash.attachments).attachments
                stashes[session] = stash
            } else {
                attach(restored, to: session)
            }
        }
        let unspool: @Sendable (SpooledAttachment) -> ComposerAttachment? = { [composerStore] in composerStore?.unspool($0) }
        var lost = saved.attachmentNames
        func unspooled(_ records: [SpooledAttachment], for session: String) -> [ComposerAttachment] {
            records.compactMap { record in
                if let attachment = unspool(record) { return attachment }
                lost[session, default: []].append(record.name)
                return nil
            }
        }
        for (session, uploads) in saved.uploadedAttachments ?? [:] { attach(uploads.map(ComposerAttachment.init(uploaded:)), to: session) }
        for (session, records) in saved.spooledDrafts ?? [:] { attach(unspooled(records, for: session), to: session) }
        for (session, uploads) in saved.stashedUploads ?? [:] { addToStash(uploads.map(ComposerAttachment.init(uploaded:)), for: session) }
        for (session, records) in saved.spooledStashes ?? [:] { addToStash(unspooled(records, for: session), for: session) }
        lostAttachments.merge(lost) { $0 + $1 }
        let resumable = saved.sends.compactMap { $0.resumable(unspool: unspool) }
        let resumed = Set(resumable.map(\.id))
        for record in saved.sends where !resumed.contains(record.id) {
            addToDraft(record.message, for: record.sessionID)
            failedSends[record.sessionID] = "Not sent: the app closed before its attachment uploaded. Add it again."
        }
        sendQueue.restore(resumable)
    }

    private func saveComposerStateSoon() {
        guard composerStore != nil, composerSave == nil else { return }
        composerSave = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(500))
            guard !Task.isCancelled else { return }
            self?.saveComposerState()
        }
    }

    func saveComposerState() {
        composerSave?.cancel()
        composerSave = nil
        guard let composerStore else { return }
        var unwritten: [ComposerAttachment] = []
        var live: Set<UUID> = []
        let spool: (ComposerAttachment) -> SpooledAttachment? = { attachment in
            live.insert(attachment.id)
            let record = composerStore.spool(attachment)
            if record == nil { unwritten.append(attachment) }
            return record
        }
        let expand = PastedTextStore.shared.expand
        var saved = ComposerStore.Saved(drafts: drafts.mapValues(expand), stashes: stashes.mapValues { expand($0.text) }, sends: sendQueue.pending.values.flatMap { $0 }.map { ComposerStore.Saved.Send($0, spool: spool) })
        if let selectedSessionID { saved.drafts[selectedSessionID] = expand(composer) }
        saved.drafts = saved.drafts.filter { !$0.value.isEmpty }
        var names = lostAttachments
        func spooled(_ attachments: [ComposerAttachment], for session: String) -> [SpooledAttachment] {
            attachments.filter { $0.uploaded == nil }.compactMap { attachment in
                if let record = spool(attachment) { return record }
                names[session, default: []].append(attachment.name)
                return nil
            }
        }
        var drafted = attachmentDrafts.filter { $0.key != selectedSessionID }
        if let selectedSessionID { drafted[selectedSessionID] = composerAttachments }
        saved.uploadedAttachments = drafted.mapValues { $0.compactMap(\.uploaded) }.filter { !$0.value.isEmpty }
        saved.stashedUploads = stashes.mapValues { $0.attachments.compactMap(\.uploaded) }.filter { !$0.value.isEmpty }
        let spooledDrafts = drafted.reduce(into: [String: [SpooledAttachment]]()) { $0[$1.key] = spooled($1.value, for: $1.key) }.filter { !$0.value.isEmpty }
        let spooledStashes = stashes.reduce(into: [String: [SpooledAttachment]]()) { $0[$1.key] = spooled($1.value.attachments, for: $1.key) }.filter { !$0.value.isEmpty }
        saved.spooledDrafts = spooledDrafts.isEmpty ? nil : spooledDrafts
        saved.spooledStashes = spooledStashes.isEmpty ? nil : spooledStashes
        saved.attachmentNames = names
        composerStore.save(saved)
        spoolTried.formIntersection(live)
        writeSpool(unwritten.filter { spoolTried.insert($0.id).inserted }, to: composerStore)
    }

    private func writeSpool(_ attachments: [ComposerAttachment], to store: ComposerStore) {
        guard !attachments.isEmpty else { return }
        Task { [weak self] in await store.write(attachments) { self?.saveComposerState() } }
    }

    private func keepAttachments(of send: PendingSend) async {
        guard let composerStore else { return }
        await composerStore.write(send.attachments) { saveComposerState() }
    }

    func announceLostAttachments(for sessionID: String) {
        guard let names = lostAttachments.removeValue(forKey: sessionID) else { return }
        attachmentProblem = "Not kept when the app closed: \(names.joined(separator: ", ")). Add again to send."
        saveComposerStateSoon()
    }
}

final class ComposerDraft: ObservableObject {
    @Published var text = ""
}
