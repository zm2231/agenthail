import Combine
import Foundation
import os

@MainActor
final class SessionPane: ObservableObject, Identifiable {
    let id = UUID()
    let restoresSelection: Bool
    unowned let model: AgenthailModel

    @Published private(set) var selectedSessionID: String? {
        didSet {
            if selectedSessionID != oldValue { endOpenInterval("left") }
        }
    }
    @Published private(set) var tabs = SessionTabs() {
        didSet {
            if restoresSelection, tabs != oldValue, let data = try? JSONEncoder().encode(tabs) {
                UserDefaults.standard.set(data, forKey: Self.tabsKey)
            }
            if tabsEnabled { syncNewSessionDraft() }
        }
    }
    @Published private(set) var newSession: NewSessionDraft?
    @Published var archivedTab: SessionState? {
        didSet { if archivedTab?.id != oldValue?.id { archivedTabError = nil } }
    }
    @Published private(set) var archivedTabError: String?
    @Published private(set) var detail: SessionDetail?
    @Published private(set) var detailStale = false
    @Published private(set) var detailRefreshFailed = false
    @Published private(set) var removedSession: SessionState?
    @Published private(set) var olderItems: [TimelineItem] = []
    @Published private(set) var olderCursor: Int64?
    @Published private(set) var loadingOlder = false
    @Published private(set) var olderError: String?
    @Published var inspectorVisible = true
    @Published var renamingSession: SessionState?
    @Published var adoptingSession: SessionState?
    @Published private(set) var forking = false
    @Published private(set) var controlPending = false
    @Published private(set) var requests: SessionRequests?
    @Published private(set) var answeringRequest: String?
    @Published var requestError: String?
    private var requestsKey: String?
    @Published private(set) var composerDraft = ComposerDraft() {
        didSet { observeDraft() }
    }
    @Published private(set) var draftIsEmpty = true
    private var draftObservation: AnyCancellable?
    var composer: String {
        get { composerDraft.text }
        set { composerDraft.text = newValue }
    }

    private var selectedSnapshot: SessionState?
    private var frozenOlderCursor: Int64?
    private var sessionStreamTask: Task<Void, Never>?
    private var sessionCursor: UInt64 = 0
    /// Rows streamed while a page read is in flight, latest mutation last, so
    /// a page read before some of them arrived can be brought up to date.
    private(set) var streamedRows: [SessionStreamEvent] = []
    private(set) var pageReadsInFlight = 0
    private var requestGeneration = 0
    /// Streamed rows apply here as they arrive and reach `detail` at most once
    /// per `streamPublishInterval`, so a burst of rows renders once.
    private(set) var unpublishedDetail: SessionDetail?
    private var publishTask: Task<Void, Never>?
    static let streamPublishInterval: Duration = .milliseconds(100)
    private var streamAwaitsCursor = false
    private var streamUnsupported = false
    private var detailReloadTask: Task<Void, Never>?
    private var detailLoadTask: Task<Void, Never>?
    private static let signposter = OSSignposter(subsystem: "com.agenthail.app", category: "SessionOpen")
    private var openInterval: (id: OSSignpostID, state: OSSignpostIntervalState)?
    @Published private(set) var detailLoadError: String?
    private var olderTask: Task<Void, Never>?
    private var metadataTask: Task<Void, Never>?
    private var metadata = MetadataOverlay()
    @Published private(set) var modelCatalogNeeded = false
    private var closed = false
    private var detailReloadPending = false
    private var detailReloadOwner: UUID?
    private var selectionGeneration: UInt64 = 0
    private var detailRequestGeneration: UInt64 = 0
    private var detailAppliedGeneration: UInt64 = 0

    init(model: AgenthailModel, restoresSelection: Bool) {
        self.model = model
        self.restoresSelection = restoresSelection
        if restoresSelection, let data = UserDefaults.standard.data(forKey: Self.tabsKey), let saved = try? JSONDecoder().decode(SessionTabs.self, from: data) {
            tabs = saved
        }
        observeDraft()
    }

    /// Keeps a draft exactly while a New Session tab is open.
    private func syncNewSessionDraft() {
        if !tabs.tabs.contains(where: { $0.id == SessionTabs.newSession }) {
            newSession = nil
        } else if newSession == nil {
            newSession = NewSessionDraft(model: model, selected: selectedSession)
        }
    }

    private static let tabsKey = "sessionTabs"

    var tabsEnabled: Bool { restoresSelection && model.interface.config.tabs }

    private func observeDraft() {
        draftObservation = composerDraft.$text.combineLatest(composerDraft.$attachments)
            .map { ComposerAttachments.isEmpty(text: $0, attachments: $1) }
            .removeDuplicates()
            .sink { [weak self] empty in
                guard let self, self.draftIsEmpty != empty else { return }
                self.draftIsEmpty = empty
            }
    }

    var selectedSession: SessionState? { model.knownSessions.first { $0.id == selectedSessionID } }
    var displayedSession: SessionState? { selectedSession ?? removedSession }
    var timelineItems: [TimelineItem] { olderItems + (detail?.timeline?.items ?? []) }

    /// Shows a session. With tabs, it opens in the preview tab unless kept,
    /// or switches to the tab it is already in.
    func select(_ id: String, keep: Bool = false) {
        let id = model.resolveSessionID(id)
        if tabsEnabled, !closed {
            tabs.open(id, keep: keep, preview: model.interface.config.tabPreview)
        } else {
            newSession = nil
        }
        show(id)
    }

    /// Shows New Session: with tabs, in its own tab, opened once.
    func openNewSession(message: String? = nil) {
        guard !closed else { return }
        if newSession == nil { newSession = NewSessionDraft(model: model, selected: selectedSession) }
        if tabsEnabled { tabs.open(SessionTabs.newSession, keep: true) }
        if let message { newSession?.take(message) }
        clearSelection()
    }

    /// Shows a session started from New Session, in the tab New Session
    /// was in.
    func showStarted(_ id: String) {
        let id = model.resolveSessionID(id)
        let shared = newSession?.takeHeld() ?? []
        if tabsEnabled, !closed {
            tabs.replace(SessionTabs.newSession, with: id)
        } else {
            newSession = nil
        }
        show(id)
        guard !shared.isEmpty, !closed else { return }
        guard tabsEnabled else {
            shared.forEach { openNewSession(message: $0) }
            return
        }
        let draft = NewSessionDraft(model: model, selected: selectedSession)
        shared.forEach(draft.take)
        newSession = draft
        tabs.add(SessionTabs.newSession)
    }

    func activateTab(_ id: String) {
        tabs.activate(id)
        showActiveTab()
    }

    func keepTab(_ id: String) {
        tabs.keep(id)
    }

    func closeTab(_ id: String) {
        let wasActive = tabs.active == id
        tabs.close(id)
        if wasActive { showActiveTab() }
    }

    /// Unarchives a session whose tab closed when it was archived, and opens
    /// its tab again. On failure the notice stays, with the reason.
    func unarchiveTab(_ session: SessionState) async {
        if archivedTab?.id == session.id { archivedTabError = nil }
        var unarchived = false
        if model.api != nil { unarchived = await model.performNow(action: "session-unarchive", sessionID: session.id) }
        if unarchived {
            if archivedTab?.id == session.id { archivedTab = nil }
            select(session.id, keep: true)
        } else if archivedTab == nil || archivedTab?.id == session.id {
            archivedTab = session
            archivedTabError = model.api == nil ? "Agenthail isn't connected, so the session stays archived." : model.operationError ?? "The session couldn't be unarchived."
        }
    }

    func closeOtherTabs(_ id: String) {
        tabs.closeOthers(id)
        showActiveTab()
    }

    func stepTab(_ offset: Int) {
        guard let next = tabs.neighbour(offset) else { return }
        tabs.activate(next)
        showActiveTab()
    }

    private func showActiveTab() {
        guard let active = tabs.active, active != SessionTabs.newSession else {
            openNewSession()
            return
        }
        show(active)
    }

    private func clearSelection() {
        guard selectedSessionID != nil || removedSession != nil else { return }
        sessionStreamTask?.cancel()
        detailReloadTask?.cancel()
        detailReloadTask = nil
        detailReloadPending = false
        removedSession = nil
        selectedSnapshot = nil
        detail = nil
        detailStale = false
        composerDraft = ComposerDraft()
        selectedSessionID = nil
    }

    /// Marks a session open in Instruments, from the switch until the
    /// daemon's first page for it is shown (`loaded`), the selection moves
    /// off it (`left`), it is removed (`removed`), the pane closes
    /// (`closed`), or it is shown again (`superseded`).
    private func beginOpenInterval(cached: Bool) {
        endOpenInterval("superseded")
        let id = Self.signposter.makeSignpostID()
        openInterval = (id, Self.signposter.beginInterval("Session open", id: id, "cached \(cached)"))
    }

    private func endOpenInterval(_ outcome: String) {
        guard let openInterval else { return }
        Self.signposter.endInterval("Session open", openInterval.state, "\(outcome, privacy: .public)")
        self.openInterval = nil
    }

    private func reconcileTabs(_ sessions: [SessionState]) {
        if tabs.tabs.isEmpty, let selected = selectedSessionID {
            tabs.open(selected, keep: true)
        }
        let byID = Dictionary(sessions.map { ($0.id, $0) }, uniquingKeysWith: { first, _ in first })
        let dropped = tabs.reconcile(resolve: model.resolveSessionID) { id in
            id == SessionTabs.newSession || byID[id].map { $0.archived != true } == true
        }
        if let archived = dropped.lazy.compactMap({ byID[self.model.resolveSessionID($0)] }).first(where: { $0.archived == true }) {
            archivedTab = archived
        }
        let shown = tabs.active == SessionTabs.newSession ? nil : tabs.active
        if shown != selectedSessionID || shown == nil && newSession == nil { showActiveTab() }
    }

    private func show(_ id: String) {
        guard !closed, id != selectedSessionID || detail == nil else { return }
        removedSession = nil
        selectedSnapshot = model.knownSessions.first { $0.id == id }
        frozenOlderCursor = nil
        composerDraft = model.draft(for: id)
        selectedSessionID = id
        model.markSeen(id)
        olderItems = []
        olderCursor = nil
        olderError = nil
        requestError = nil
        streamedRows = []
        selectionGeneration &+= 1
        detailAppliedGeneration = detailRequestGeneration
        if restoresSelection { UserDefaults.standard.set(id, forKey: "lastSelectedSessionID") }
        dropUnpublishedDetail()
        detail = model.cachedDetail(id)
        detailStale = detail != nil
        beginOpenInterval(cached: detail != nil)
        detailRefreshFailed = false
        detailLoadError = nil
        detailReloadPending = false
        detailReloadTask?.cancel()
        detailReloadTask = nil
        metadata = MetadataOverlay(seed: detail)
        awaitStreamCursor()
        startDetailLoad(id)
        startMetadataLoad(id)
    }

    func close() {
        closed = true
        endOpenInterval("closed")
        streamAwaitsCursor = false
        sessionStreamTask?.cancel()
        detailReloadTask?.cancel()
        detailLoadTask?.cancel()
        olderTask?.cancel()
        metadataTask?.cancel()
        dropUnpublishedDetail()
        sessionStreamTask = nil
        detailReloadTask = nil
        detailLoadTask = nil
        olderTask = nil
        selectionGeneration &+= 1
        detailAppliedGeneration = detailRequestGeneration
    }

    @discardableResult
    func loadSession(_ id: String) async -> Bool {
        guard !closed, let api = model.api else { return false }
        detailRequestGeneration &+= 1
        let generation = detailRequestGeneration
        pageReadsInFlight += 1
        defer {
            pageReadsInFlight -= 1
            if pageReadsInFlight == 0 { streamedRows = [] }
        }
        do {
            let loaded = try await api.sessionDetail(id: id, includeTimeline: true)
            guard sessionLoadIsCurrent(id, selectedID: selectedSessionID), generation > detailAppliedGeneration, removedSession?.id != id else { return true }
            detailAppliedGeneration = generation
            let shown = unpublishedDetail ?? detail
            let previous = shown?.session.id == id ? shown?.timeline : nil
            let fresh = Set(loaded.timeline?.items.map(\.id) ?? [])
            if let previous, let shared = previous.items.firstIndex(where: { fresh.contains($0.id) }) {
                // Only rows before the page's start fell off it; later rows it lacks are newer than it.
                if olderItems.isEmpty { olderCursor = previous.nextBefore }
                let retained = fresh.union(olderItems.map(\.id))
                olderItems += previous.items[..<shared].filter { !retained.contains($0.id) }
            } else if olderItems.isEmpty || previous?.items.isEmpty == false {
                olderItems = []
                olderCursor = loaded.timeline?.nextBefore
            }
            metadata.absorb(context: loaded.context, goal: loaded.goal, model: loaded.model, models: loaded.models)
            var merged = metadata.apply(to: loaded)
            let pageSeq = loaded.journalSeq ?? 0
            var older = olderItems
            for row in streamedRows where row.seq > pageSeq { merged = Self.applying(row, to: merged, older: &older) ?? merged }
            if older != olderItems { olderItems = older }
            dropUnpublishedDetail()
            detail = merged
            endOpenInterval("loaded")
            detailStale = false
            detailRefreshFailed = false
            model.detailLoaded(merged, for: id)
            detailLoadError = nil
            if streamAwaitsCursor {
                streamAwaitsCursor = false
                startSessionStream(id, after: loaded.journalSeq ?? 0)
            }
            return true
        } catch {
            guard !closed, sessionLoadIsCurrent(id, selectedID: selectedSessionID), removedSession?.id != id, !error.isCancellation else { return true }
            detailLoadError = AgenthailAPIError.userMessage(error)
            if let openInterval { Self.signposter.emitEvent("Session open failed", id: openInterval.id) }
            if detail?.session.id == id {
                detailStale = true
                detailRefreshFailed = true
            }
            return false
        }
    }

    func loadOlder() async {
        guard !closed, olderTask == nil else { return }
        let task = Task { await fetchOlder() }
        olderTask = task
        await task.value
        olderTask = nil
    }

    private func fetchOlder() async {
        guard let api = model.api, let id = selectedSessionID, !detailStale, let cursor = olderCursor, cursor > 0, !loadingOlder else { return }
        let selection = selectionGeneration
        loadingOlder = true
        defer { loadingOlder = false }
        do {
            var next: Int64? = cursor
            for _ in 0..<8 {
                guard let before = next, before > 0 else { break }
                let page = try await api.sessionDetail(id: id, includeTimeline: true, timelineBefore: before)
                guard selectionGeneration == selection else { return }
                guard let timeline = page.timeline, timeline.unavailableReason == nil else {
                    olderError = page.timeline?.unavailableReason ?? "Older activity is unavailable."
                    return
                }
                let known = Set(timelineItems.map(\.id))
                let fresh = timeline.items.filter { !known.contains($0.id) }
                olderItems = fresh + olderItems
                next = timeline.nextBefore
                olderCursor = next
                if !fresh.isEmpty { break }
            }
            olderError = nil
        } catch {
            guard selectionGeneration == selection, !error.isCancellation else { return }
            if case AgenthailAPIError.historyGap = error { olderCursor = nil }
            olderError = AgenthailAPIError.userMessage(error)
        }
    }

    func reconcile() {
        guard !closed, model.snapshot != nil else { return }
        let sessions = model.knownSessions
        if tabsEnabled {
            reconcileTabs(sessions)
        }
        if let selected = selectedSessionID, model.resolveSessionID(selected) != selected {
            show(model.resolveSessionID(selected))
            return
        }
        if let selected = selectedSessionID {
            if let live = sessions.first(where: { $0.id == selected }) {
                selectedSnapshot = live
                if streamUnsupported, live.capabilities.stream, removedSession == nil {
                    awaitStreamCursor()
                    startDetailLoad(selected)
                }
                if removedSession != nil {
                    removedSession = nil
                    olderCursor = frozenOlderCursor
                    frozenOlderCursor = nil
                    awaitStreamCursor()
                    startDetailLoad(selected)
                    startMetadataLoad(selected)
                    Task { await syncRequests(for: live) }
                }
            } else if removedSession?.id != selected, selectedSnapshot?.id == selected {
                removedSession = selectedSnapshot
                freezeRemovedSession()
            }
            if removedSession != nil || sessions.contains(where: { $0.id == selected }) { return }
        }
        guard restoresSelection, !tabsEnabled, newSession == nil else { return }
        let next = reconciledSelection(selected: (selectedSessionID ?? UserDefaults.standard.string(forKey: "lastSelectedSessionID")).map(model.resolveSessionID), sessions: sessions)
        guard next != selectedSessionID else {
            if next == nil { openNewSession() }
            return
        }
        sessionStreamTask?.cancel()
        detailReloadTask?.cancel()
        detailReloadTask = nil
        detailReloadPending = false
        dropUnpublishedDetail()
        detail = nil
        detailStale = false
        composerDraft = ComposerDraft()
        selectedSessionID = nil
        if let next { show(next) } else { openNewSession() }
    }

    func closeRemovedSession() {
        guard removedSession != nil else { return }
        removedSession = nil
        selectedSnapshot = nil
        frozenOlderCursor = nil
        composerDraft = ComposerDraft()
        sessionStreamTask?.cancel()
        dropUnpublishedDetail()
        detail = nil
        detailStale = false
        selectedSessionID = nil
        if restoresSelection { UserDefaults.standard.removeObject(forKey: "lastSelectedSessionID") }
        reconcile()
    }

    func sessionChanged(_ id: String) {
        scheduleDetailReload(id)
    }

    func changeModel(to modelID: String) {
        guard let sessionID = selectedSessionID else { return }
        var settings = model.turnSettings(for: sessionID)
        settings.effort = nil
        model.setTurnSettings(settings, for: sessionID)
        runControl(action: "model", sessionID: sessionID, modelID: modelID)
    }

    func compactContext() {
        guard let sessionID = selectedSessionID else { return }
        runControl(action: "compact", sessionID: sessionID, modelID: nil)
    }

    private func runControl(action: String, sessionID: String, modelID: String?) {
        guard !controlPending, removedSession == nil else { return }
        controlPending = true
        Task {
            let succeeded = await model.performNow(action: action, sessionID: sessionID, model: modelID)
            controlPending = false
            if succeeded, !closed, selectedSessionID == sessionID, removedSession == nil { startMetadataLoad(sessionID) }
        }
    }

    func submit(busyDelivery: String?) {
        guard let sessionID = selectedSessionID, removedSession == nil else { return }
        if composerDraft.attachments.isEmpty, let session = selectedSession {
            let input = SlashInput(composer, available: SlashCommand.available(capabilities: session.capabilities, working: session.isWorking, waitingOnYou: session.isWaitingOnYou))
            if let problem = input.problem {
                model.operationError = problem
                return
            }
            if case let .command(command, argument) = input {
                composer = ""
                run(command, argument: argument, sessionID: sessionID)
                return
            }
        }
        let text = composer
        let attachments = composerDraft.attachments
        composer = ""
        composerDraft.attachments = []
        tabs.keep(sessionID)
        send(text, attachments: attachments, to: sessionID, busyDelivery: busyDelivery)
        composerDraft.recordSending(text)
        composerDraft.restoreStashAfterSend()
    }

    func resend(_ text: String, attachments: [ComposerAttachment] = []) {
        guard let sessionID = selectedSessionID, removedSession == nil else { return }
        send(text, attachments: attachments, to: sessionID, busyDelivery: nil)
    }

    private func send(_ text: String, attachments: [ComposerAttachment] = [], to sessionID: String, busyDelivery: String?) {
        let settings = selectedSession?.surface == "codex" && busyDelivery != "steer" ? model.turnSettings(for: sessionID) : TurnSettings()
        model.send(text, attachments: attachments, to: sessionID, busyDelivery: busyDelivery, turnSettings: settings)
    }

    private func run(_ command: SlashCommand, argument: String, sessionID: String) {
        model.operationError = nil
        switch command {
        case .name:
            if let problem = SessionHandle.problem(with: argument) {
                model.operationError = problem
                composer = command.token + " " + argument
                return
            }
            Task {
                if let failure = await model.rename(sessionID, to: SessionHandle.normalized(argument)) { model.operationError = failure }
            }
        case .compact: compactContext()
        case .stop: interrupt()
        case .steer: model.send(argument, to: sessionID, busyDelivery: "steer")
        case .model: changeModel(to: resolvedModelID(argument))
        case .goal: model.perform(action: "goal-set", sessionID: sessionID, message: argument)
        }
    }

    func resolvedModelID(_ query: String) -> String {
        let options = detail?.models ?? []
        return options.first { $0.id.caseInsensitiveCompare(query) == .orderedSame || $0.displayName.caseInsensitiveCompare(query) == .orderedSame }?.id ?? query
    }

    func forkAgain(_ session: SessionState) {
        model.actionKeys.abandon(.fork(session.id))
        fork(session)
    }

    func fork(_ session: SessionState, before prompt: PromptItem? = nil) {
        guard !forking, removedSession == nil, SessionOperationAvailability.fork(session) else { return }
        forking = true
        Task { await forkNow(session, before: prompt) }
    }

    func forkNow(_ session: SessionState, before prompt: PromptItem? = nil) async {
        defer { forking = false }
        guard let api = model.api else {
            model.operationError = "Agenthail isn't connected."
            return
        }
        model.pendingForks.remove(session.id)
        do {
            let forked = try await api.forkSession(id: session.id, before: prompt?.turnId, keys: model.actionKeys)
            model.operationError = nil
            if let prompt { model.draft(for: forked.id).restore(prompt.text) }
            await model.openCreatedSession(forked.id, in: closed ? nil : self)
        } catch AgenthailAPIError.outcomeUnknown {
            model.operationError = nil
            model.pendingForks.insert(session.id)
        } catch {
            model.operationError = AgenthailAPIError.userMessage(error)
        }
    }

    static func stopAvailable(_ session: SessionState?, removed: Bool, draftEmpty: Bool) -> Bool {
        guard let session, !removed, !session.isReadOnly, session.isWorking else { return false }
        return draftEmpty
    }

    var canStop: Bool { Self.stopAvailable(displayedSession, removed: removedSession != nil, draftEmpty: draftIsEmpty) }

    func interrupt() {
        guard let sessionID = selectedSessionID, removedSession == nil else { return }
        model.perform(action: "interrupt", sessionID: sessionID)
    }

    private func freezeRemovedSession() {
        endOpenInterval("removed")
        streamAwaitsCursor = false
        sessionStreamTask?.cancel()
        dropUnpublishedDetail()
        metadataTask?.cancel()
        detailLoadTask?.cancel()
        detailLoadTask = nil
        requestsKey = nil
        requestGeneration &+= 1
        detailReloadTask?.cancel()
        detailReloadTask = nil
        detailReloadPending = false
        detailAppliedGeneration = detailRequestGeneration
        selectionGeneration &+= 1
        frozenOlderCursor = olderCursor
        olderCursor = nil
        detailStale = false
        detailRefreshFailed = false
    }

    private func startMetadataLoad(_ id: String) {
        metadataTask?.cancel()
        metadata.metadataRequested()
        modelCatalogNeeded = false
        guard !closed, let api = model.api else { return }
        let selection = selectionGeneration
        metadataTask = Task {
            let loaded = try? await api.sessionMetadata(id: id)
            guard !Task.isCancelled, !closed, selectionGeneration == selection, selectedSessionID == id, removedSession == nil else { return }
            if let loaded { metadata.absorb(loaded) } else { metadata.metadataFailed() }
            modelCatalogNeeded = metadata.needsModelCatalog
            applyMetadata(to: id)
        }
    }

    private func applyMetadata(to id: String) {
        guard let current = unpublishedDetail ?? detail, current.session.id == id else { return }
        let merged = metadata.apply(to: current)
        dropUnpublishedDetail()
        detail = merged
        model.refreshCachedDetail(merged, for: id)
    }

    /// Changes whenever syncRequests could reach a different result, so the
    /// view resyncs when requests become unavailable as well as when they move.
    static func requestSyncKey(_ session: SessionState) -> String {
        "\(session.id)|\(session.waitingOn?.id ?? "")|\(session.capabilities.respond)|\(session.isReadOnly)"
    }

    func syncRequests(for session: SessionState) async {
        guard session.capabilities.respond, !session.isReadOnly else {
            requests = nil
            requestsKey = nil
            requestError = nil
            return
        }
        let key = "\(session.id)|\(session.waitingOn?.id ?? "")"
        guard requestsKey != key else { return }
        if requestsKey?.hasPrefix(session.id + "|") != true { requests = nil }
        requestsKey = key
        await fetchRequests(session.id, key: key)
    }

    private func fetchRequests(_ id: String, key: String?) async {
        guard let api = model.api else { return }
        requestGeneration &+= 1
        let generation = requestGeneration
        do {
            let loaded = try await api.requests(sessionID: id)
            guard requestGeneration == generation, selectedSessionID == id, requestsKey == key, removedSession == nil else { return }
            requests = loaded
            requestError = nil
        } catch {
            guard requestGeneration == generation, selectedSessionID == id, removedSession == nil else { return }
            requestsKey = nil
            requestError = error.localizedDescription
        }
    }

    func respond(to request: SessionRequest, decision: RequestDecision) async {
        guard let api = model.api, let id = selectedSessionID, answeringRequest == nil else { return }
        answeringRequest = request.id
        defer { answeringRequest = nil }
        do {
            _ = try await api.respond(sessionID: id, requestID: request.id, decision: decision)
            requestError = nil
        } catch AgenthailAPIError.requestStale {
            requests = requests?.removing(request.id)
            requestError = "That request was already answered or has expired."
        } catch {
            requestError = error.localizedDescription
        }
        await fetchRequests(id, key: requestsKey)
        await loadSession(id)
    }

    func setPermissionMode(_ mode: String) async {
        guard let api = model.api, let id = selectedSessionID else { return }
        do {
            try await api.setPermissionMode(sessionID: id, mode: mode)
            requestError = nil
        } catch {
            requestError = error.localizedDescription
        }
        await fetchRequests(id, key: requestsKey)
    }

    func reloadDetail() {
        if let selectedSessionID { scheduleDetailReload(selectedSessionID) }
    }

    private func startDetailLoad(_ id: String) {
        detailLoadTask?.cancel()
        detailLoadTask = Task {
            let backoff = EventRetryBackoff()
            while !Task.isCancelled, !closed, selectedSessionID == id {
                if await loadSession(id) { return }
                try? await Task.sleep(for: .seconds(backoff.nextDelay()))
            }
        }
    }

    private func scheduleDetailReload(_ id: String) {
        guard !closed, selectedSessionID == id, removedSession == nil else { return }
        detailReloadPending = true
        guard detailReloadTask == nil else { return }
        let owner = UUID()
        detailReloadOwner = owner
        detailReloadTask = Task {
            defer {
                if detailReloadOwner == owner { detailReloadTask = nil }
            }
            while detailReloadPending, !Task.isCancelled, selectedSessionID == id {
                try? await Task.sleep(for: .milliseconds(250))
                guard !Task.isCancelled, selectedSessionID == id else { return }
                detailReloadPending = false
                await loadSession(id)
            }
        }
    }

    private func awaitStreamCursor() {
        sessionStreamTask?.cancel()
        sessionStreamTask = nil
        streamAwaitsCursor = true
        streamUnsupported = false
    }

    private func startSessionStream(_ id: String, after cursor: UInt64) {
        sessionStreamTask?.cancel()
        guard !closed else { return }
        sessionCursor = cursor
        sessionStreamTask = Task {
            let backoff = EventRetryBackoff()
            while !Task.isCancelled, selectedSessionID == id {
                guard let api = model.api else { return }
                do {
                    try await api.streamSession(id: id, after: sessionCursor, onConnected: {}, onEvent: { [weak self] event in
                        backoff.reset()
                        await self?.receiveSessionEvent(event)
                    })
                } catch {
                    if Task.isCancelled || selectedSessionID != id { return }
                    if case AgenthailAPIError.streamUnsupported = error, selectedSnapshot?.capabilities.stream != true {
                        // A session gains its stream once its transcript exists; reconcile restarts it then.
                        streamUnsupported = true
                        return
                    }
                    if case AgenthailAPIError.streamGap = error {
                        streamAwaitsCursor = true
                        startDetailLoad(id)
                        return
                    }
                    try? await Task.sleep(for: .seconds(backoff.nextDelay()))
                }
            }
        }
    }

    private func receiveSessionEvent(_ event: SessionStreamEvent) {
        guard event.stream == "session", event.sessionId == selectedSessionID else { return }
        sessionCursor = max(sessionCursor, event.seq)
        if event.data.kind == "context" || event.data.kind == "goal" {
            guard removedSession == nil else { return }
            if event.data.kind == "context" { metadata.stream(context: event.data.context) } else { metadata.stream(goal: event.data.goal) }
            applyMetadata(to: event.sessionId)
            applyStreamedRow(event)
            return
        }
        guard applyStreamedRow(event) else {
            scheduleDetailReload(event.sessionId)
            return
        }
        // A finished turn reloads once, so exchanges and paging come from the daemon.
        if event.data.kind == "done" { scheduleDetailReload(event.sessionId) }
    }

    @discardableResult
    private func applyStreamedRow(_ event: SessionStreamEvent) -> Bool {
        // A row that leaves the earlier pages alone must not publish them, or
        // every row re-renders the window.
        var older = olderItems
        guard removedSession == nil, let current = unpublishedDetail ?? detail, current.session.id == event.sessionId,
              let updated = Self.applying(event, to: current, older: &older) else { return false }
        if older != olderItems { olderItems = older }
        if pageReadsInFlight > 0 {
            streamedRows.removeAll { $0.data.itemId == event.data.itemId && !event.data.itemId.isEmpty }
            streamedRows.append(event)
        }
        unpublishedDetail = updated
        guard publishTask == nil else { return true }
        publishTask = Task { [weak self] in
            try? await Task.sleep(for: Self.streamPublishInterval)
            guard let self, !Task.isCancelled else { return }
            publishTask = nil
            guard let pending = unpublishedDetail else { return }
            unpublishedDetail = nil
            detail = pending
            model.detailLoaded(pending, for: pending.session.id)
        }
        return true
    }

    private func dropUnpublishedDetail() {
        publishTask?.cancel()
        publishTask = nil
        unpublishedDetail = nil
    }

    /// Applies a streamed journal row as a session page reads it: an updated
    /// row keeps its place, as its journal entry does, and a new row goes at
    /// the end, so a later reload shows the same rows in the same order.
    /// Returns nil for a row it cannot apply.
    static func applying(_ event: SessionStreamEvent, to detail: SessionDetail, older: inout [TimelineItem]) -> SessionDetail? {
        guard var timeline = detail.timeline else { return nil }
        let id = event.data.itemId
        if event.data.kind == "source-error" {
            timeline.unavailableReason = event.data.reason
        } else if event.data.op == "remove" {
            timeline.items.removeAll { $0.id == id }
            older.removeAll { $0.id == id }
        } else if let item = event.item {
            if let index = timeline.items.firstIndex(where: { $0.id == item.id }) {
                timeline.items[index] = item
            } else if let index = older.firstIndex(where: { $0.id == item.id }) {
                older[index] = item
            } else {
                timeline.items.append(item)
            }
            timeline.unavailableReason = nil
        } else {
            return nil
        }
        var updated = detail
        updated.timeline = timeline
        return updated
    }
}
