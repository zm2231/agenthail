import Foundation

@MainActor
final class SessionPane: ObservableObject, Identifiable {
    let id = UUID()
    let restoresSelection: Bool
    unowned let model: AgenthailModel

    @Published private(set) var selectedSessionID: String?
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
    @Published private(set) var controlPending = false
    @Published private(set) var composerDraft = ComposerDraft()
    var composer: String {
        get { composerDraft.text }
        set { composerDraft.text = newValue }
    }

    private var selectedSnapshot: SessionState?
    private var frozenOlderCursor: Int64?
    private var sessionStreamTask: Task<Void, Never>?
    private var sessionCursor: UInt64 = 0
    private var detailReloadTask: Task<Void, Never>?
    private var detailLoadTask: Task<Void, Never>?
    private var olderTask: Task<Void, Never>?
    private var metadataTask: Task<Void, Never>?
    private var metadata = MetadataOverlay()
    @Published private(set) var modelCatalogNeeded = false
    private var closed = false
    private var detailReloadPending = false
    private var detailReloadOwner: UUID?
    private var detailLoadedAt: Date?
    private var selectionGeneration: UInt64 = 0
    private var detailRequestGeneration: UInt64 = 0
    private var detailAppliedGeneration: UInt64 = 0

    init(model: AgenthailModel, restoresSelection: Bool) {
        self.model = model
        self.restoresSelection = restoresSelection
    }

    var selectedSession: SessionState? { model.knownSessions.first { $0.id == selectedSessionID } }
    var displayedSession: SessionState? { selectedSession ?? removedSession }
    var timelineItems: [TimelineItem] { olderItems + (detail?.timeline?.items ?? []) }

    func select(_ id: String) {
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
        selectionGeneration &+= 1
        detailAppliedGeneration = detailRequestGeneration
        if restoresSelection { UserDefaults.standard.set(id, forKey: "lastSelectedSessionID") }
        detail = model.cachedDetail(id)
        detailStale = detail != nil
        detailRefreshFailed = false
        detailLoadedAt = nil
        detailReloadPending = false
        detailReloadTask?.cancel()
        detailReloadTask = nil
        metadata = MetadataOverlay(seed: detail)
        startSessionStream(id)
        startDetailLoad(id)
        startMetadataLoad(id)
    }

    func close() {
        closed = true
        sessionStreamTask?.cancel()
        detailReloadTask?.cancel()
        detailLoadTask?.cancel()
        olderTask?.cancel()
        metadataTask?.cancel()
        sessionStreamTask = nil
        detailReloadTask = nil
        detailLoadTask = nil
        olderTask = nil
        selectionGeneration &+= 1
        detailAppliedGeneration = detailRequestGeneration
    }

    func loadSession(_ id: String) async {
        guard !closed, let api = model.api else { return }
        detailRequestGeneration &+= 1
        let generation = detailRequestGeneration
        do {
            let startedAt = Date()
            if selectedSessionID == id { detailLoadedAt = startedAt }
            let loaded = try await api.sessionDetail(id: id, includeTimeline: true)
            guard sessionLoadIsCurrent(id, selectedID: selectedSessionID), generation > detailAppliedGeneration, removedSession?.id != id else { return }
            detailAppliedGeneration = generation
            if olderItems.isEmpty {
                olderCursor = loaded.timeline?.nextBefore
            } else {
                let retained = Set(olderItems.map(\.id) + (loaded.timeline?.items.map(\.id) ?? []))
                olderItems += (detail?.timeline?.items ?? []).filter { !retained.contains($0.id) }
            }
            metadata.absorb(context: loaded.context, goal: loaded.goal, model: loaded.model, models: loaded.models)
            let merged = metadata.apply(to: loaded)
            detail = merged
            detailStale = false
            detailRefreshFailed = false
            model.detailLoaded(merged, for: id)
        } catch {
            guard !closed, sessionLoadIsCurrent(id, selectedID: selectedSessionID), !error.isCancellation else { return }
            model.operationError = error.localizedDescription
            if detailStale { detailRefreshFailed = true }
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
            olderError = error.localizedDescription
        }
    }

    func reconcile() {
        guard !closed, model.snapshot != nil else { return }
        let sessions = model.knownSessions
        if let selected = selectedSessionID {
            if let live = sessions.first(where: { $0.id == selected }) {
                selectedSnapshot = live
                if removedSession != nil {
                    removedSession = nil
                    olderCursor = frozenOlderCursor
                    frozenOlderCursor = nil
                    startSessionStream(selected)
                    startDetailLoad(selected)
                    startMetadataLoad(selected)
                }
            } else if removedSession?.id != selected, selectedSnapshot?.id == selected {
                removedSession = selectedSnapshot
                freezeRemovedSession()
            }
            if removedSession != nil || sessions.contains(where: { $0.id == selected }) { return }
        }
        guard restoresSelection else { return }
        let next = reconciledSelection(selected: selectedSessionID ?? UserDefaults.standard.string(forKey: "lastSelectedSessionID"), sessions: sessions)
        guard next != selectedSessionID else { return }
        sessionStreamTask?.cancel()
        detailReloadTask?.cancel()
        detailReloadTask = nil
        detailReloadPending = false
        detail = nil
        detailStale = false
        composerDraft = ComposerDraft()
        selectedSessionID = nil
        if let next { select(next) }
    }

    func closeRemovedSession() {
        guard removedSession != nil else { return }
        removedSession = nil
        selectedSnapshot = nil
        frozenOlderCursor = nil
        composerDraft = ComposerDraft()
        sessionStreamTask?.cancel()
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
            if succeeded, !closed, selectedSessionID == sessionID { startMetadataLoad(sessionID) }
        }
    }

    func submit(busyDelivery: String?) {
        guard let sessionID = selectedSessionID, removedSession == nil else { return }
        let text = composer
        composer = ""
        let settings = selectedSession?.surface == "codex" && busyDelivery != "steer" ? model.turnSettings(for: sessionID) : TurnSettings()
        model.send(text, to: sessionID, busyDelivery: busyDelivery, turnSettings: settings)
    }

    static func stopAvailable(_ session: SessionState?, removed: Bool, draft: String) -> Bool {
        guard let session, !removed, !session.isReadOnly, session.isWorking else { return false }
        return draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    var canStop: Bool { Self.stopAvailable(displayedSession, removed: removedSession != nil, draft: composer) }

    func interrupt() {
        guard let sessionID = selectedSessionID, removedSession == nil else { return }
        model.perform(action: "interrupt", sessionID: sessionID)
    }

    private func freezeRemovedSession() {
        sessionStreamTask?.cancel()
        metadataTask?.cancel()
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
        guard let current = detail, current.session.id == id else { return }
        let merged = metadata.apply(to: current)
        detail = merged
        model.refreshCachedDetail(merged, for: id)
    }

    private func startDetailLoad(_ id: String) {
        detailLoadTask?.cancel()
        detailLoadTask = Task { await loadSession(id) }
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

    private func startSessionStream(_ id: String) {
        sessionStreamTask?.cancel()
        guard !closed else { return }
        sessionCursor = 0
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
                    if case AgenthailAPIError.streamGap = error {
                        sessionCursor = 0
                        scheduleDetailReload(id)
                        continue
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
            return
        }
        if let loadedAt = detailLoadedAt, let changedAt = SessionTree.parseTimestamp(event.data.ts), changedAt < loadedAt {
            return
        }
        scheduleDetailReload(event.sessionId)
    }
}
