import AppKit
import Foundation

@MainActor
final class AgenthailModel: ObservableObject {
    @Published var snapshot: DashboardSnapshot?
    @Published var selectedSessionID: String?
    @Published var detail: SessionDetail?
    @Published var devices: [DeviceState] = []
    @Published var pairing: PairingResponse?
    @Published var settings: DashboardSettingsState?
    @Published var audit: [HistoryState] = []
    @Published var auditKinds: [String] = []
    @Published var auditHasMore = false
    @Published var auditQuery = ""
    @Published var auditKind = ""
    @Published var connectionError: String?
    @Published var reconnecting = false
    @Published var operationError: String?
    @Published var loading = false
    @Published var composer = ""
    @Published var operationsVisible = false
    @Published var sessionFilter: SessionFilter = .recent
    @Published var inspectorVisible = true
    @Published private(set) var localSends: [String: [LocalSend]] = [:]

    private var api: AgenthailAPI?
    private var connectionTask: Task<Void, Never>?
    private var eventTask: Task<Void, Never>?
    private var refreshTask: Task<Void, Never>?
    private var statusRefreshTask: Task<Void, Never>?
    private var lastEventID: UInt64 = 0
    private var catalogStreamTask: Task<Void, Never>?
    private var sessionStreamTask: Task<Void, Never>?
    private var catalogPosition = CatalogPosition(epoch: nil, cursor: 0)
    private var sessionCursor: UInt64 = 0
    private var detailReloadTask: Task<Void, Never>?
    private var detailLoadedAt: Date?
    private var detailReloadPending = false

    var isConnected: Bool { connectionError == nil && snapshot?.daemon.running == true }
    var currentSessions: [SessionState] { snapshot?.sessions.filter(\.current) ?? [] }
    var workingSessions: [SessionState] { snapshot?.sessions.filter(\.isWorking) ?? [] }
    var selectedSession: SessionState? { snapshot?.sessions.first { $0.id == selectedSessionID } }
    var attentionSessionIDs: Set<String> { Set(snapshot?.attention.map(\.sessionId) ?? []) }
    var sessionTree: SessionTree {
        SessionTree.build(snapshot?.sessions ?? [], filter: sessionFilter, attentionSessionIDs: attentionSessionIDs, now: Date())
    }

    init() {
        connect()
    }

    deinit {
        connectionTask?.cancel()
        eventTask?.cancel()
        refreshTask?.cancel()
        statusRefreshTask?.cancel()
        catalogStreamTask?.cancel()
        sessionStreamTask?.cancel()
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
                    connectionError = nil
                    lastEventID = 0
                    guard await refresh(fresh: true) else {
                        throw AgenthailAPIError.unavailable(connectionError ?? "Agenthail could not connect.")
                    }
                    startEvents()
                    startStatusRefresh()
                    return
                } catch {
                    connectionError = error.localizedDescription
                    snapshot = nil
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
            if catalogPosition.adopt(snapshotEpoch: loaded.hostEpoch, snapshotSeq: loaded.catalogSeq), catalogStreamTask != nil {
                startCatalogStream()
            }
            lastEventID = max(lastEventID, loaded.eventCursor ?? lastEventID)
            clearConnectionError()
            reconcileSelection()
        } catch {
            connectionError = error.localizedDescription
            setLoading(false)
            return false
        }
        setLoading(false)
        return true
    }

    func selectSession(_ id: String) {
        guard id != selectedSessionID || detail == nil else { return }
        selectedSessionID = id
        detail = nil
        detailLoadedAt = nil
        detailReloadPending = false
        detailReloadTask?.cancel()
        detailReloadTask = nil
        startSessionStream(id)
        Task { await loadSession(id) }
    }

    func loadSession(_ id: String) async {
        guard let api else { return }
        do {
            let startedAt = Date()
            if selectedSessionID == id { detailLoadedAt = startedAt }
            let loaded = try await api.sessionDetail(id: id, includeTimeline: true)
            guard sessionLoadIsCurrent(id, selectedID: selectedSessionID) else { return }
            detail = loaded
            if let items = loaded.timeline?.items, let sends = localSends[id] {
                localSends[id] = LocalSend.reconcile(sends, with: items)
            }
            operationError = nil
        } catch {
            guard sessionLoadIsCurrent(id, selectedID: selectedSessionID) else { return }
            operationError = error.localizedDescription
        }
    }

    private func reconcileSelection() {
        guard let sessions = snapshot?.sessions else { return }
        let next = reconciledSelection(selected: selectedSessionID, sessions: sessions)
        guard next != selectedSessionID else { return }
        sessionStreamTask?.cancel()
        detailReloadTask?.cancel()
        detailReloadTask = nil
        detailReloadPending = false
        detail = nil
        selectedSessionID = nil
        if let next { selectSession(next) }
    }

    private func scheduleDetailReload(_ id: String) {
        guard selectedSessionID == id else { return }
        detailReloadPending = true
        guard detailReloadTask == nil else { return }
        detailReloadTask = Task {
            defer { detailReloadTask = nil }
            while detailReloadPending, !Task.isCancelled, selectedSessionID == id {
                try? await Task.sleep(for: .milliseconds(250))
                guard !Task.isCancelled, selectedSessionID == id else { return }
                detailReloadPending = false
                await loadSession(id)
            }
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
                if let message, composer.isEmpty { composer = message }
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
                operationError = message
                return
            }
            try? await Task.sleep(for: .seconds(2))
            connect()
        }
    }

    func submit(_ message: String, steer: Bool) {
        let text = message.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, let api, let sessionID = selectedSessionID else { return }
        let pending = LocalSend(text: text, sentAt: Date(), status: nil)
        localSends[sessionID, default: []].append(pending)
        Task {
            do {
                let receipt = try await api.sendInstruction(action: steer ? "steer" : "send", sessionID: sessionID, message: text)
                updateLocalSend(pending.id, in: sessionID, status: LocalSend.label(for: receipt.result?.status))
                operationError = nil
            } catch {
                localSends[sessionID]?.removeAll { $0.id == pending.id }
                if composer.isEmpty { composer = text }
                operationError = error.localizedDescription
            }
        }
    }

    func interruptSelected() {
        guard let sessionID = selectedSessionID else { return }
        perform(action: "interrupt", sessionID: sessionID)
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
        case "surface.health":
            guard let name = event.data.surface, let health = event.data.health, let index = current.surfaces.firstIndex(where: { $0.name == name }) else { return }
            let previous = current.surfaces[index]
            current.surfaces[index] = SurfaceState(name: name, connected: health == "healthy", error: health == "healthy" ? nil : event.data.detail, health: health, healthDetail: event.data.detail, capabilities: previous.capabilities)
        default:
            return
        }
        current.totalSessions = current.sessions.count
        snapshot = current
        reconcileSelection()
    }

    private func startSessionStream(_ id: String) {
        sessionStreamTask?.cancel()
        sessionCursor = 0
        sessionStreamTask = Task {
            let backoff = EventRetryBackoff()
            while !Task.isCancelled, selectedSessionID == id {
                guard let api else { return }
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

    func receiveSessionEvent(_ event: SessionStreamEvent) {
        guard event.stream == "session", event.sessionId == selectedSessionID else { return }
        sessionCursor = max(sessionCursor, event.seq)
        if let loadedAt = detailLoadedAt, let changedAt = SessionTree.parseTimestamp(event.data.ts), changedAt < loadedAt {
            return
        }
        scheduleDetailReload(event.sessionId)
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
                        connectionError = nil
                    } catch {
                        if Task.isCancelled { return }
                        connectionError = error.localizedDescription
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
        refreshTask?.cancel()
        refreshTask = Task {
            try? await Task.sleep(for: .milliseconds(180))
            if Task.isCancelled { return }
            _ = await refresh()
            if let entityID = event.entityId, entityID == selectedSessionID {
                scheduleDetailReload(entityID)
            }
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

    private func setLoading(_ value: Bool) {
        if loading != value { loading = value }
    }

    private func clearConnectionError() {
        if connectionError != nil { connectionError = nil }
    }
}
