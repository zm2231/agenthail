import AppKit
import Foundation

@MainActor
final class AgenthailModel: ObservableObject {
    @Published var snapshot: DashboardSnapshot? {
        didSet {
            trackFinishedSessions(from: oldValue)
            pruneDetailCache()
        }
    }
    @Published private(set) var finishedUnseen: Set<String> = []
    @Published private(set) var snapshotLoadedAt: Date?
    @Published var newSessionVisible = false
    @Published var paletteVisible = false
    @Published private(set) var creationOptions: SessionCreationOptions?
    @Published var searchQuery = ""
    @Published private(set) var searchResults: [SessionSearchItem] = []
    @Published private(set) var searching = false
    @Published private(set) var searchError: String?
    @Published private(set) var pinnedSessions: [SessionState] = []
    private var searchTask: Task<Void, Never>?
    @Published var devices: [DeviceState] = []
    @Published var pairing: PairingResponse?
    @Published var settings: DashboardSettingsState?
    @Published var audit: [HistoryState] = []
    @Published var auditKinds: [String] = []
    @Published var auditHasMore = false
    @Published var auditQuery = ""
    @Published var auditKind = ""
    @Published var connectionError: String?
    @Published private(set) var daemonSlow = false
    @Published var reconnecting = false
    @Published var operationError: String?
    @Published var loading = false
    @Published var operationsVisible = false
    @Published var sessionFilter: SessionFilter = .recent
    @Published private(set) var localSends: [String: [LocalSend]] = [:]

    private(set) var api: AgenthailAPI?
    private(set) var mainPane: SessionPane!
    private var panes: [SessionPane] = []
    private var visibleWindows = 0
    private var connectionTask: Task<Void, Never>?
    private var eventTask: Task<Void, Never>?
    private var refreshTask: Task<Void, Never>?
    private var statusRefreshTask: Task<Void, Never>?
    private var lastEventID: UInt64 = 0
    private var catalogStreamTask: Task<Void, Never>?
    private var catalogPosition = CatalogPosition(epoch: nil, cursor: 0)
    private var detailCache: [String: SessionDetail] = [:]
    private var detailCacheOrder: [String] = []
    private var drafts: [String: ComposerDraft] = [:]
    private let attachmentCache: NSCache<NSString, NSData> = {
        let cache = NSCache<NSString, NSData>()
        cache.totalCostLimit = 64 * 1024 * 1024
        cache.countLimit = 100
        return cache
    }()

    var isConnected: Bool { connectionError == nil && snapshot?.daemon.running == true }
    var currentSessions: [SessionState] { snapshot?.sessions.filter(\.current) ?? [] }
    var workingSessions: [SessionState] { snapshot?.sessions.filter(\.isWorking) ?? [] }
    var knownSessions: [SessionState] {
        let listed = snapshot?.sessions ?? []
        let listedIDs = Set(listed.map(\.id))
        return listed + pinnedSessions.filter { !listedIDs.contains($0.id) }
    }
    var deliveryProblems: [DeliveryProblem] { snapshot?.deliveryProblems ?? [] }
    var attentionSessionIDs: Set<String> { Set((snapshot?.attention.map(\.sessionId) ?? []) + deliveryProblems.map(\.sessionId)) }
    var sessionTree: SessionTree {
        SessionTree.build(knownSessions, filter: sessionFilter, attentionSessionIDs: attentionSessionIDs, now: Date())
    }

    init() {
        mainPane = SessionPane(model: self, restoresSelection: true)
        panes = [mainPane]
        connect()
    }

    func openPane() -> SessionPane {
        let pane = SessionPane(model: self, restoresSelection: false)
        panes.append(pane)
        return pane
    }

    func windowAppeared() {
        visibleWindows += 1
        guard visibleWindows == 1 else { return }
        NSApplication.shared.setActivationPolicy(.regular)
        NSApplication.shared.activate()
    }

    func windowDisappeared() {
        visibleWindows = max(0, visibleWindows - 1)
        if visibleWindows == 0 { NSApplication.shared.setActivationPolicy(.accessory) }
    }

    func closePane(_ pane: SessionPane) {
        guard pane !== mainPane else { return }
        pane.close()
        panes.removeAll { $0 === pane }
    }

    deinit {
        connectionTask?.cancel()
        eventTask?.cancel()
        refreshTask?.cancel()
        statusRefreshTask?.cancel()
        catalogStreamTask?.cancel()
    }

    func connect() {
        eventTask?.cancel()
        connectionTask?.cancel()
        statusRefreshTask?.cancel()
        connectionTask = Task {
            let backoff = EventRetryBackoff()
            while !Task.isCancelled {
                do {
                    let api = try AgenthailAPI()
                    _ = try await api.version()
                    self.api = api
                    clearConnectionError()
                    lastEventID = 0
                    if await refresh(fresh: true) {
                        startEvents()
                        startStatusRefresh()
                        return
                    }
                } catch {
                    connectionFailed(error)
                    if let apiError = error as? AgenthailAPIError, case .incompatible = apiError {
                        return
                    }
                }
                let delay = backoff.nextDelay()
                try? await Task.sleep(for: .seconds(delay))
            }
        }
    }

    @discardableResult
    func refresh(fresh: Bool = false) async -> Bool {
        guard let api else { return false }
        setLoading(snapshot == nil)
        do {
            let loaded = try await api.snapshot(fresh: fresh)
            if snapshot?.hasSamePresentation(as: loaded) != true {
                snapshot = loaded
            }
            snapshotLoadedAt = Date()
            if catalogPosition.adopt(snapshotEpoch: loaded.hostEpoch, snapshotSeq: loaded.catalogSeq), catalogStreamTask != nil {
                startCatalogStream()
            }
            lastEventID = max(lastEventID, loaded.eventCursor ?? lastEventID)
            clearConnectionError()
            reconcilePanes()
        } catch {
            connectionFailed(error)
            setLoading(false)
            return false
        }
        setLoading(false)
        return true
    }

    func cachedDetail(_ id: String) -> SessionDetail? {
        guard let cached = detailCache[id] else { return nil }
        touchCachedDetail(id)
        return cached
    }

    func detailLoaded(_ loaded: SessionDetail, for id: String) {
        cacheDetail(loaded, for: id)
        if let items = loaded.timeline?.items, let sends = localSends[id] {
            localSends[id] = LocalSend.reconcile(sends, with: items)
        }
        operationError = nil
    }

    func draft(for sessionID: String) -> ComposerDraft {
        if let draft = drafts[sessionID] { return draft }
        let draft = ComposerDraft()
        drafts[sessionID] = draft
        return draft
    }

    func markSeen(_ id: String) {
        finishedUnseen.remove(id)
    }

    private func cacheDetail(_ loaded: SessionDetail, for id: String) {
        detailCache[id] = loaded
        touchCachedDetail(id)
        while detailCacheOrder.count > 16 {
            detailCache.removeValue(forKey: detailCacheOrder.removeFirst())
        }
    }

    private func touchCachedDetail(_ id: String) {
        detailCacheOrder.removeAll { $0 == id }
        detailCacheOrder.append(id)
    }

    private func pruneDetailCache() {
        guard snapshot != nil else { return }
        let known = Set(knownSessions.map(\.id))
        detailCacheOrder.removeAll { !known.contains($0) }
        detailCache = detailCache.filter { known.contains($0.key) }
    }

    private func trackFinishedSessions(from previous: DashboardSnapshot?) {
        guard let previous, let current = snapshot else { return }
        let wasWorking = Set(previous.sessions.filter(\.isWorking).map(\.id))
        var next = finishedUnseen
        for session in current.sessions {
            if session.isWorking {
                next.remove(session.id)
            } else if wasWorking.contains(session.id), !panes.contains(where: { $0.selectedSessionID == session.id }) {
                next.insert(session.id)
            }
        }
        next.formIntersection(current.sessions.map(\.id))
        if next != finishedUnseen { finishedUnseen = next }
    }

    func loadCreationOptions() async {
        guard let api else { return }
        do {
            creationOptions = try await api.sessionOptions()
        } catch {
            if !error.isCancellation { operationError = error.localizedDescription }
        }
    }

    func launchSession(launcher: String?, agent: String, folder: String, message: String) async -> String? {
        guard let api else { return "Agenthail isn't connected." }
        do {
            let receipt = try await api.launchSession(launcher: launcher, surface: agent, message: message, cwd: folder)
            if receipt.unknown == true {
                if let id = receipt.id { await openCreatedSession(id) }
                return "Agenthail couldn't confirm the session started. Check the sidebar before trying again."
            }
            guard receipt.ok else { return receipt.error ?? "The session didn't start." }
            if let id = receipt.id { await openCreatedSession(id) }
            operationError = nil
            return nil
        } catch {
            return error.localizedDescription
        }
    }

    private func openCreatedSession(_ id: String) async {
        await refresh(fresh: true)
        if !knownSessions.contains(where: { $0.id == id }), let api, let detail = try? await api.sessionDetail(id: id) {
            pin(SessionState(id: detail.session.id, surface: detail.session.surface, name: detail.session.name, alias: detail.alias, status: detail.session.status, lastActive: detail.session.lastActive, queueCount: 0, open: true, current: false, currentReason: nil, capabilities: detail.capabilities, readOnly: detail.readOnly, readOnlyReason: detail.readOnlyReason, cwd: detail.session.cwd))
        }
        if knownSessions.contains(where: { $0.id == id }) { mainPane.select(id) }
    }

    func attachmentData(sessionID: String, attachment: TimelineAttachment) async throws -> Data {
        let key = "\(sessionID)/\(attachment.id)" as NSString
        if let cached = attachmentCache.object(forKey: key) { return cached as Data }
        guard let api else { throw AgenthailAPIError.invalidResponse }
        let data = try await api.attachmentData(sessionID: sessionID, attachmentID: attachment.id)
        attachmentCache.setObject(data as NSData, forKey: key, cost: data.count)
        return data
    }

    func focusInTerminal(_ session: SessionState) {
        perform(action: "session-focus", sessionID: session.id)
    }

    func search(_ query: String) {
        searchQuery = query
        searchTask?.cancel()
        searchResults = []
        searchError = nil
        searching = false
        let trimmed = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard trimmed.count >= 3, let api else { return }
        searching = true
        searchTask = Task {
            try? await Task.sleep(for: .milliseconds(350))
            guard !Task.isCancelled else { return }
            do {
                let response = try await api.searchSessions(query: trimmed)
                guard !Task.isCancelled, searchQuery == query else { return }
                searchResults = response.results
                searchError = response.remoteError?.isEmpty == false ? response.remoteError : nil
            } catch {
                guard !Task.isCancelled, searchQuery == query, !error.isCancellation else { return }
                searchError = error.localizedDescription
            }
            if searchQuery == query { searching = false }
        }
    }

    func openNotifiedSession(_ id: String) {
        if knownSessions.contains(where: { $0.id == id }) {
            mainPane.select(id)
        } else {
            Task { await openCreatedSession(id) }
        }
    }

    func openSearchResult(_ session: SessionState) {
        pin(session)
        mainPane.select(session.id)
    }

    private func pin(_ session: SessionState) {
        if !(snapshot?.sessions.contains { $0.id == session.id } ?? false), !pinnedSessions.contains(where: { $0.id == session.id }) {
            pinnedSessions.append(session)
        }
    }

    func perform(action: String, sessionID: String? = nil, message: String? = nil, model: String? = nil, queueID: Int64? = nil) {
        guard let api else { return }
        Task {
            do {
                try await api.action(action, sessionID: sessionID, message: message, model: model, queueID: queueID)
                operationError = nil
                await refresh(fresh: true)
            } catch {
                operationError = error.localizedDescription
                if let message, let sessionID { restoreToComposer(message, sessionID: sessionID) }
            }
        }
    }

    func loadDevices() async {
        guard let api else { return }
        do {
            devices = try await api.devices()
        } catch {
            operationError = error.localizedDescription
        }
    }

    func loadOperations() async {
        await loadDevices()
        guard let api else { return }
        do {
            settings = try await api.settings()
            await loadAudit(reset: true)
        } catch {
            operationError = error.localizedDescription
        }
    }

    func loadAudit(reset: Bool) async {
        guard let api else { return }
        do {
            let before = reset ? 0 : (audit.last?.id ?? 0)
            let page = try await api.history(before: before, kind: auditKind, query: auditQuery)
            audit = reset ? page.items : audit + page.items
            auditKinds = page.kinds
            auditHasMore = page.hasMore
            operationError = nil
        } catch {
            operationError = error.localizedDescription
        }
    }

    func performOperation(action: String, message: String? = nil, channel: String? = nil, targetID: String? = nil, fromID: String? = nil, toID: String? = nil, pattern: String? = nil, relayID: Int64? = nil) {
        guard let api else { return }
        Task {
            do {
                try await api.action(action, message: message, channel: channel, targetID: targetID, fromID: fromID, toID: toID, pattern: pattern, relayID: relayID)
                operationError = nil
                _ = await refresh(fresh: true)
                await loadAudit(reset: true)
            } catch {
                operationError = error.localizedDescription
            }
        }
    }

    func updateNotifications(_ action: String) {
        guard let api else { return }
        Task {
            do {
                try await api.updateSettings(action: action)
                settings = try await api.settings()
                operationError = nil
            } catch {
                operationError = error.localizedDescription
                settings = try? await api.settings()
            }
        }
    }

    func setRemoteAccess(_ enabled: Bool) {
        guard let api else { return }
        Task {
            do {
                try await api.updateSettings(action: enabled ? "remote-enable" : "remote-disable")
                settings = try await api.settings()
                pairing = nil
                operationError = nil
            } catch {
                operationError = error.localizedDescription
            }
        }
    }

    func createPairing() {
        guard let api else { return }
        Task {
            do {
                pairing = try await api.createPairing(name: "iPhone")
                operationError = nil
            } catch {
                operationError = error.localizedDescription
            }
        }
    }

    func revokeDevice(_ id: String) {
        guard let api else { return }
        Task {
            do {
                try await api.revokeDevice(id: id)
                await loadDevices()
            } catch {
                operationError = error.localizedDescription
            }
        }
    }

    func restartDaemon() {
        Task {
            let (status, output) = await Task.detached {
                AgenthailProcess.output(["daemon", "restart"])
            }.value
            guard status == 0 else {
                let failure = output.trimmingCharacters(in: .whitespacesAndNewlines)
                let message = failure.isEmpty ? "Agenthail could not restart." : failure
                connectionError = message
                daemonSlow = false
                operationError = message
                return
            }
            try? await Task.sleep(for: .seconds(2))
            connect()
        }
    }

    func send(_ message: String, to sessionID: String, steer: Bool) {
        let text = message.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, let api else { return }
        let pending = LocalSend(text: text, sentAt: Date(), status: nil)
        localSends[sessionID, default: []].append(pending)
        Task {
            do {
                let receipt = try await api.sendInstruction(action: steer ? "steer" : "send", sessionID: sessionID, message: text)
                updateLocalSend(pending.id, in: sessionID, status: LocalSend.label(for: receipt.result?.status))
                operationError = nil
            } catch {
                localSends[sessionID]?.removeAll { $0.id == pending.id }
                restoreToComposer(text, sessionID: sessionID)
                operationError = error.localizedDescription
            }
        }
    }

    func queuedItems(for sessionID: String) -> [QueueState] {
        (snapshot?.queue ?? []).filter { $0.sessionId == sessionID && $0.status == "pending" && ["", "message"].contains($0.operation ?? "") }
    }

    func steerQueued(_ item: QueueState) {
        guard let api else { return }
        Task {
            do {
                try await api.action("queue-cancel", queueID: item.id)
            } catch {
                operationError = error.localizedDescription
                return
            }
            do {
                _ = try await api.sendInstruction(action: "steer", sessionID: item.sessionId, message: item.message)
                operationError = nil
            } catch {
                restoreToComposer(item.message, sessionID: item.sessionId)
                operationError = "Steer may not have reached the agent. The message is back in the composer; check the conversation before resending. \(error.localizedDescription)"
            }
            _ = await refresh(fresh: true)
        }
    }

    func removeQueued(_ item: QueueState, restoreToComposer: Bool = false) {
        guard let api else { return }
        Task {
            do {
                try await api.action("queue-cancel", queueID: item.id)
                if restoreToComposer { self.restoreToComposer(item.message, sessionID: item.sessionId) }
                operationError = nil
            } catch {
                operationError = error.localizedDescription
            }
            _ = await refresh(fresh: true)
        }
    }

    func deliveryProblems(for sessionID: String) -> [DeliveryProblem] {
        deliveryProblems.filter { $0.sessionId == sessionID || $0.sourceSessionId == sessionID }
    }

    func dismissDeliveryProblem(_ problem: DeliveryProblem) {
        guard let api else { return }
        removeDeliveryProblem(problem.deliveryId)
        Task {
            do {
                try await api.action("delivery-dismiss", deliveryID: problem.deliveryId)
                operationError = nil
            } catch {
                operationError = error.localizedDescription
                _ = await refresh(fresh: true)
            }
        }
    }

    private func removeDeliveryProblem(_ deliveryID: Int64) {
        snapshot?.deliveryProblems?.removeAll { $0.deliveryId == deliveryID }
    }

    func resendDeliveryProblem(_ problem: DeliveryProblem) {
        dismissDeliveryProblem(problem)
        restoreToComposer(problem.message, sessionID: problem.sessionId)
    }

    private func restoreToComposer(_ text: String, sessionID: String) {
        draft(for: sessionID).restore(text)
    }

    private func updateLocalSend(_ id: UUID, in sessionID: String, status: String) {
        guard let index = localSends[sessionID]?.firstIndex(where: { $0.id == id }) else { return }
        localSends[sessionID]?[index].status = status
    }

    private func startCatalogStream() {
        catalogStreamTask?.cancel()
        catalogStreamTask = Task {
            let backoff = EventRetryBackoff()
            while !Task.isCancelled {
                guard let api else { return }
                do {
                    try await api.streamCatalog(after: catalogPosition.cursor, onConnected: {}, onEvent: { [weak self] event in
                        backoff.reset()
                        await self?.applyCatalogEvent(event)
                    })
                } catch {
                    if Task.isCancelled { return }
                    if case AgenthailAPIError.streamGap = error {
                        catalogPosition = CatalogPosition(epoch: nil, cursor: 0)
                        _ = await refresh(fresh: true)
                        continue
                    }
                    try? await Task.sleep(for: .seconds(backoff.nextDelay()))
                }
            }
        }
    }

    func applyCatalogEvent(_ event: CatalogStreamEvent) {
        guard event.stream == "catalog", var current = snapshot, catalogPosition.accept(event.seq) else { return }
        switch event.type {
        case "session.upserted":
            guard let changed = event.data.session else { return }
            if let index = current.sessions.firstIndex(where: { $0.id == changed.id }) {
                current.sessions[index] = changed
            } else {
                current.sessions.append(changed)
            }
        case "session.removed":
            guard let id = event.data.sessionId else { return }
            current.sessions.removeAll { $0.id == id }
        case "delivery.problem":
            guard let deliveryID = event.data.deliveryId, let sessionID = event.data.sessionId else { return }
            let problem = DeliveryProblem(deliveryId: deliveryID, sessionId: sessionID, sourceSessionId: event.data.sourceSessionId, message: event.data.message ?? "", reason: event.data.reason ?? "", at: event.data.at)
            var problems = current.deliveryProblems ?? []
            problems.removeAll { $0.deliveryId == deliveryID }
            problems.insert(problem, at: 0)
            current.deliveryProblems = problems
        case "delivery.dismissed":
            guard let deliveryID = event.data.deliveryId else { return }
            current.deliveryProblems?.removeAll { $0.deliveryId == deliveryID }
        case "surface.health":
            guard let name = event.data.surface, let health = event.data.health, let index = current.surfaces.firstIndex(where: { $0.name == name }) else { return }
            let previous = current.surfaces[index]
            current.surfaces[index] = SurfaceState(name: name, connected: health == "healthy", error: health == "healthy" ? nil : event.data.detail, health: health, healthDetail: event.data.detail, capabilities: previous.capabilities)
        default:
            return
        }
        current.totalSessions = current.sessions.count
        snapshot = current
        reconcilePanes()
    }

    private func startEvents() {
        eventTask?.cancel()
        eventTask = Task {
            let backoff = EventRetryBackoff()
            while !Task.isCancelled {
                guard let api else { return }
                do {
                    try await api.streamEvents(
                        after: lastEventID,
                        onConnected: { [weak self] in await self?.eventStreamConnected() },
                        onEvent: { [weak self] event in
                            backoff.reset()
                            await self?.receive(event)
                        }
                    )
                } catch {
                    if Task.isCancelled { return }
                    reconnecting = true
                    do {
                        let reloadedAPI = try AgenthailAPI()
                        _ = try await reloadedAPI.version()
                        if Task.isCancelled { return }
                        self.api = reloadedAPI
                        clearConnectionError()
                    } catch {
                        if Task.isCancelled { return }
                        connectionFailed(error)
                        if let apiError = error as? AgenthailAPIError, case .incompatible = apiError {
                            reconnecting = false
                            return
                        }
                    }
                    let delay = backoff.nextDelay()
                    try? await Task.sleep(for: .seconds(delay))
                }
            }
        }
    }

    private func startStatusRefresh() {
        statusRefreshTask?.cancel()
        statusRefreshTask = Task {
            while !Task.isCancelled {
                try? await Task.sleep(for: StatusRefreshPolicy.interval(reconnecting: reconnecting))
                if Task.isCancelled { return }
                _ = await refresh()
            }
        }
    }

    private func receive(_ event: AgenthailEvent) {
        if event.type == "stream.reset" {
            lastEventID = 0
        } else {
            lastEventID = max(lastEventID, event.id)
        }
        if let entityID = event.entityId {
            panes.forEach { $0.sessionChanged(entityID) }
        }
        refreshTask?.cancel()
        refreshTask = Task {
            try? await Task.sleep(for: .milliseconds(180))
            if Task.isCancelled { return }
            _ = await refresh()
            if OperationsRefreshPolicy.reloadOperations(for: event.type) {
                await loadOperations()
            } else if operationsVisible && OperationsRefreshPolicy.reloadAudit(for: event.type) {
                await loadAudit(reset: true)
            }
        }
    }

    private func eventStreamConnected() {
        reconnecting = false
        clearConnectionError()
        startCatalogStream()
    }

    private func reconcilePanes() {
        panes.forEach { $0.reconcile() }
    }

    private func setLoading(_ value: Bool) {
        if loading != value { loading = value }
    }

    private func clearConnectionError() {
        if connectionError != nil { connectionError = nil }
        if daemonSlow { daemonSlow = false }
    }

    private func connectionFailed(_ error: Error) {
        connectionError = error.localizedDescription
        daemonSlow = error.isTimeout
    }
}

extension Error {
    var isCancellation: Bool {
        self is CancellationError || (self as? URLError)?.code == .cancelled
    }

    var isTimeout: Bool {
        (self as? URLError)?.code == .timedOut
    }
}

@MainActor
final class ComposerDraft: ObservableObject {
    @Published var text = ""

    func restore(_ restored: String) {
        text = text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? restored : "\(text)\n\n\(restored)"
    }
}
