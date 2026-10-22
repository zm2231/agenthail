import AppKit
import Foundation

@MainActor
final class AgenthailModel: ObservableObject {
    @Published var snapshot: DashboardSnapshot? {
        didSet {
            trackFinishedSessions(from: oldValue)
            pruneDetailCache()
            if let snapshot {
                HolderPins.shared.prune(to: snapshot.sessions)
                holderPicks = HolderPins.shared.all
            }
            if snapshot != nil, let reference = pendingSessionReference {
                pendingSessionReference = nil
                openSession(reference: reference)
            }
        }
    }
    @Published private(set) var finishedUnseen: Set<String> = []
    @Published private(set) var holderPicks: [String: HolderPin] = [:]
    @Published private(set) var snapshotLoadedAt: Date?
    @Published var newSessionVisible = false
    @Published var newSessionMessage: String?
    @Published var paletteVisible = false
    @Published var searchQuery = ""
    @Published private(set) var searchResults: [SessionSearchItem] = []
    @Published private(set) var searching = false
    @Published private(set) var searchError: String?
    @Published private(set) var pinnedSessions: [SessionState] = []
    private var searchTask: Task<Void, Never>?
    @Published var devices: [DeviceState] = []
    @Published var signin: SigninState?
    @Published var permissions: PermissionsState?
    @Published var permissionsError: String?
    @Published var signinError: String?
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
    @Published var pendingForks: Set<String> = []
    @Published var loading = false
    @Published var operationsVisible = false
    @Published var sessionFilter: SessionFilter = .recent
    @Published var sessionRefinement = SessionRefinement()
    @Published private(set) var localSends: [String: [LocalSend]] = [:]
    @Published private var turnSettingsDrafts: [String: TurnSettings] = [:]
    @Published private(set) var fetchedSubagents = SubagentPageCache()

    private(set) var api: AgenthailAPI?
    let actionKeys = ActionRetryKeys()
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
    private var rekeys = SessionRekeys()
    private var pendingSessionReference: String?
    private let attachmentCache: NSCache<NSString, NSData> = {
        let cache = NSCache<NSString, NSData>()
        cache.totalCostLimit = 64 * 1024 * 1024
        cache.countLimit = 100
        return cache
    }()

    var isConnected: Bool { connectionError == nil && snapshot?.daemon.running == true }
    var currentSessions: [SessionState] { snapshot?.sessions.filter(\.current) ?? [] }
    var knownSessions: [SessionState] {
        let listed = snapshot?.sessions ?? []
        let listedIDs = Set(listed.map(\.id))
        return listed + pinnedSessions.filter { !listedIDs.contains($0.id) }
    }
    var deliveryProblems: [DeliveryProblem] { snapshot?.deliveryProblems ?? [] }
    var attentionSessionIDs: Set<String> { Set((snapshot?.attention.map(\.sessionId) ?? []) + deliveryProblems.compactMap(\.attentionSessionId)) }
    var sessionTree: SessionTree {
        SessionTree.build(knownSessions, filter: sessionFilter, refinement: sessionRefinement, attentionSessionIDs: attentionSessionIDs)
    }

    init(connecting: Bool = true, api: AgenthailAPI? = nil) {
        self.api = api
        mainPane = SessionPane(model: self, restoresSelection: true)
        panes = [mainPane]
        if connecting { connect() }
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

    func pickHolder(_ holder: SessionHolder?, for sessionID: String) {
        HolderPins.shared.pick(holder.map(HolderPin.init), for: sessionID)
        holderPicks = HolderPins.shared.all
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
                        startCatalogStream()
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

    /// Fetches the direct subagents a session list only counts, so they can
    /// be shown and opened.
    func loadSubagents(of session: SessionState) async {
        guard let api else { return }
        guard (session.hiddenSubagents?.count ?? 0) > 0 else {
            fetchedSubagents.drop(session.id)
            return
        }
        do {
            let loaded = try await api.subagents(of: session.id)
            if !Task.isCancelled { fetchedSubagents.store(loaded, for: session.id) }
        } catch {
            if !Task.isCancelled { fetchedSubagents.fail(AgenthailAPIError.userMessage(error), for: session.id) }
        }
    }

    func subagents(of sessionID: String) -> [SessionState] {
        SessionFamilies.children(of: sessionID, listed: knownSessions, fetched: fetchedSubagents.children(of: sessionID))
    }

    @discardableResult
    func refresh(fresh: Bool = false) async -> Bool {
        guard let api else { return false }
        setLoading(snapshot == nil)
        do {
            let loaded = try await api.snapshot(fresh: fresh)
            adopt(loaded)
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

    // Rekeys are recorded before the snapshot is published, because publishing
    // opens a pending notification target and wakes a waiting reply.
    func adopt(_ loaded: DashboardSnapshot) {
        rekeys.record(loaded.rekeyed ?? [:])
        if snapshot?.hasSamePresentation(as: loaded) != true {
            snapshot = loaded
        }
        snapshotLoadedAt = Date()
    }

    func refreshNow(_ pane: SessionPane? = nil) {
        Task {
            guard await refresh(fresh: true) else { return }
            operationError = nil
            (pane ?? mainPane).reloadDetail()
        }
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
    }

    func refreshCachedDetail(_ detail: SessionDetail, for id: String) {
        guard detailCache[id] != nil else { return }
        detailCache[id] = detail
    }

    func receiveSharedText(_ text: String) {
        if !newSessionVisible, let session = mainPane.selectedSession, mainPane.removedSession == nil, !session.isReadOnly, session.capabilities.send {
            draft(for: session.id).restore(text)
        } else {
            newSessionMessage = text
            newSessionVisible = true
        }
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
        let wasWorking = Set(previous.sessions.filter { $0.isWorking || $0.isWaitingOnBackground }.map(\.id))
        var next = finishedUnseen
        for session in current.sessions {
            if session.isWorking || session.isWaitingOnBackground {
                next.remove(session.id)
            } else if wasWorking.contains(session.id), !panes.contains(where: { $0.selectedSessionID == session.id }) {
                next.insert(session.id)
            }
        }
        next.formIntersection(current.sessions.map(\.id))
        if next != finishedUnseen { finishedUnseen = next }
    }

    func creationOptions(host: String?) async throws -> SessionCreationOptions {
        guard let api else { throw AgenthailAPIError.unavailable("Agenthail isn't connected.") }
        return try await api.sessionOptions(host: host)
    }

    func creationModels(for agent: String, host: String?) async throws -> [ModelOption] {
        guard let api else { throw AgenthailAPIError.unavailable("Agenthail isn't connected.") }
        return try await api.creationModels(surface: agent, host: host)
    }

    func launchSession(_ form: SessionLaunchForm) async -> SessionLaunchOutcome {
        let outcome = await createSession(form)
        if !outcome.keepsRetryKey { actionKeys.abandon(.creation) }
        return outcome
    }

    func startNewLaunch() {
        actionKeys.abandon(.creation)
    }

    private func createSession(_ form: SessionLaunchForm) async -> SessionLaunchOutcome {
        guard let api else { return .failed("Agenthail isn't connected.") }
        do {
            let body = try form.creationBody()
            let receipt = try await api.createSession(body: body, idempotencyKey: actionKeys.key(for: body, scope: .creation), failureReceipts: true)
            let decision = SessionLaunchDecision(receipt, launcher: form.launcher?.id, agent: form.agent)
            switch decision {
            case .open(let id):
                operationError = nil
                await openCreatedSession(id)
                return .opened
            case .submitted(let note):
                operationError = nil
                return .submitted(note)
            case .starting:
                return .starting("Agenthail is still starting this session. It appears in the sidebar once it starts.")
            case .unconfirmed(let id):
                if let id { await openCreatedSession(id) }
                return .unconfirmed("This session may still be starting. It will appear in the sidebar when ready.")
            case .halted(let message):
                return .halted(message)
            case .failed(let message):
                return .failed(message)
            }
        } catch AgenthailAPIError.request(_, let message) {
            return .failed(message)
        } catch AgenthailAPIError.unavailable(let message) {
            return .failed(message)
        } catch let error as URLError where ActionRetryKeys.notSent.contains(error.code) {
            return .failed(AgenthailAPIError.userMessage(error))
        } catch {
            return .uncertain(error.localizedDescription)
        }
    }

    func openCreatedSession(_ id: String, in pane: SessionPane? = nil) async {
        await refresh(fresh: true)
        if !knownSessions.contains(where: { AgenthailLink.matches($0, reference: id) }), let api, let detail = try? await api.sessionDetail(id: id) {
            var created = SessionState(id: detail.session.id, surface: detail.session.surface, name: detail.session.name, alias: detail.alias.flatMap { $0.isEmpty ? nil : $0 }, status: detail.session.status, lastActive: detail.session.lastActive, queueCount: 0, open: true, current: false, currentReason: nil, capabilities: detail.capabilities, readOnly: detail.readOnly, readOnlyReason: detail.readOnlyReason, cwd: detail.session.cwd)
            created.host = snapshot?.daemon.pairedHost(owning: detail.session.id)
            pin(created)
        }
        if let session = knownSessions.first(where: { AgenthailLink.matches($0, reference: id) }) { (pane ?? mainPane).select(session.id) }
    }

    func attachmentData(sessionID: String, attachment: SessionAttachment) async throws -> Data {
        let key = "\(sessionID)/\(attachment.id)" as NSString
        if let cached = attachmentCache.object(forKey: key) { return cached as Data }
        guard let api else { throw AgenthailAPIError.invalidResponse }
        let data = try await api.sessionAttachment(sessionID: sessionID, id: attachment.id)
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
                searchError = AgenthailAPIError.userMessage(error)
            }
            if searchQuery == query { searching = false }
        }
    }

    func openSession(reference: String) {
        guard snapshot != nil else {
            pendingSessionReference = reference
            return
        }
        let reference = rekeys.resolve(reference)
        if let session = knownSessions.first(where: { AgenthailLink.matches($0, reference: reference) }) {
            mainPane.select(session.id)
        } else {
            Task { await openCreatedSession(reference) }
        }
    }

    func openSearchResult(_ session: SessionState) {
        pin(session)
        mainPane.select(session.id)
    }

    func openSubagent(_ session: SessionState, in pane: SessionPane) {
        pin(session)
        pane.select(session.id)
    }

    private func pin(_ session: SessionState) {
        if !(snapshot?.sessions.contains { $0.id == session.id } ?? false), !pinnedSessions.contains(where: { $0.id == session.id }) {
            pinnedSessions.append(session)
        }
    }

    func perform(action: String, sessionID: String? = nil, message: String? = nil, model: String? = nil, queueID: Int64? = nil) {
        Task { await performNow(action: action, sessionID: sessionID, message: message, model: model, queueID: queueID) }
    }

    @discardableResult
    func performNow(action: String, sessionID: String? = nil, message: String? = nil, model: String? = nil, queueID: Int64? = nil) async -> Bool {
        guard let api else { return false }
        do {
            try await api.action(action, sessionID: sessionID, message: message, model: model, queueID: queueID)
            operationError = nil
            _ = await refresh(fresh: true)
            return true
        } catch {
            operationError = AgenthailAPIError.userMessage(error)
            if let message, let sessionID { restoreToComposer(message, sessionID: sessionID) }
            return false
        }
    }

    func loadDevices() async {
        guard let api else { return }
        do {
            devices = try await api.devices()
        } catch {
            operationError = AgenthailAPIError.userMessage(error)
        }
    }

    func loadOperations() async {
        await loadDevices()
        guard let api else { return }
        do {
            settings = try await api.settings()
            await loadAudit(reset: true)
        } catch {
            operationError = AgenthailAPIError.userMessage(error)
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
            operationError = AgenthailAPIError.userMessage(error)
        }
    }

    func performOperation(action: String, message: String? = nil, channel: String? = nil, targetID: String? = nil, fromID: String? = nil, toID: String? = nil, pattern: String? = nil, once: Bool? = nil, handoff: String? = nil, relayID: Int64? = nil) {
        guard let api else { return }
        Task {
            do {
                try await api.action(action, message: message, channel: channel, targetID: targetID, fromID: fromID, toID: toID, pattern: pattern, once: once, handoff: handoff, relayID: relayID)
                operationError = nil
                _ = await refresh(fresh: true)
                await loadAudit(reset: true)
            } catch {
                operationError = AgenthailAPIError.userMessage(error)
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
                operationError = AgenthailAPIError.userMessage(error)
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
                operationError = AgenthailAPIError.userMessage(error)
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
                operationError = AgenthailAPIError.userMessage(error)
            }
        }
    }

    func loadPermissions() async {
        guard let api else { return }
        do {
            permissions = try await api.permissions()
            permissionsError = nil
        } catch {
            permissionsError = AgenthailAPIError.userMessage(error)
        }
    }

    /// Has macOS ask for each grant the daemon lacks; the prompts show on
    /// this Mac, and the grants are read again once the person is back.
    func requestPermissions() async {
        guard let api else { return }
        do {
            try await api.requestPermissions()
            permissionsError = nil
        } catch {
            permissionsError = AgenthailAPIError.userMessage(error)
        }
    }

    func loadSignin() async {
        guard let api else { return }
        do {
            signin = try await api.signin()
            signinError = nil
        } catch {
            signinError = AgenthailAPIError.userMessage(error)
        }
    }

    func addSigninProfile(browser: String, profile: String, label: String) {
        guard let api else { return }
        Task {
            do {
                _ = try await api.addSigninProfile(browser: browser, profile: profile, label: label)
                await loadSignin()
            } catch {
                signinError = AgenthailAPIError.userMessage(error)
            }
        }
    }

    func bindSignin(consumer: String, choice: String) {
        guard let api else { return }
        Task {
            do {
                _ = try await api.bindSignin(consumer: consumer, choice: choice)
                await loadSignin()
            } catch {
                signinError = AgenthailAPIError.userMessage(error)
                await loadSignin()
            }
        }
    }

    func removeSigninProfile(_ id: String) {
        guard let api else { return }
        Task {
            do {
                try await api.removeSigninProfile(id: id)
                await loadSignin()
            } catch {
                signinError = AgenthailAPIError.userMessage(error)
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
                operationError = AgenthailAPIError.userMessage(error)
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

    func reply(_ message: String, to sessionID: String, connectionTimeout: Duration = .seconds(20)) async -> Bool {
        let text = message.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return true }
        guard await awaitConnection(timeout: connectionTimeout) else {
            restoreToComposer(text, sessionID: rekeys.resolve(sessionID))
            return false
        }
        let sessionID = rekeys.resolve(sessionID)
        return await withCheckedContinuation { continuation in
            send(text, to: sessionID) { continuation.resume(returning: $0) }
        }
    }

    private func awaitConnection(timeout: Duration) async -> Bool {
        if api != nil, snapshot != nil { return true }
        let waiter = Task { @MainActor in
            for await value in $snapshot.values where value != nil { return true }
            return false
        }
        let timer = Task {
            try? await Task.sleep(for: timeout)
            waiter.cancel()
        }
        let connected = await waiter.value
        timer.cancel()
        return connected && api != nil
    }

    func turnSettings(for sessionID: String) -> TurnSettings {
        turnSettingsDrafts[sessionID] ?? TurnSettings()
    }

    func setTurnSettings(_ settings: TurnSettings, for sessionID: String) {
        if settings.isEmpty { turnSettingsDrafts.removeValue(forKey: sessionID) } else { turnSettingsDrafts[sessionID] = settings }
    }

    func send(_ message: String, attachments: [ComposerAttachment] = [], to sessionID: String, busyDelivery: String? = nil, turnSettings: TurnSettings = .init(), completion: ((Bool) -> Void)? = nil) {
        let text = message.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !ComposerAttachments.isEmpty(text: text, attachments: attachments), let api else {
            restoreToComposer(text, attachments: attachments, sessionID: sessionID)
            completion?(false)
            return
        }
        let pending = LocalSend(text: text, attachments: attachments, sentAt: Date(), status: nil, busyDelivery: busyDelivery, turnSettings: turnSettings)
        localSends[sessionID, default: []].append(pending)
        deliver(pending, to: sessionID, api: api, completion: completion)
    }

    func retrySend(_ send: LocalSend, in sessionID: String) {
        guard let api, let index = localSends[sessionID]?.firstIndex(where: { $0.id == send.id }) else { return }
        localSends[sessionID]?[index].failure = nil
        deliver(send, to: sessionID, api: api)
    }

    private func deliver(_ send: LocalSend, to sessionID: String, api: AgenthailAPI, completion: ((Bool) -> Void)? = nil) {
        Task {
            do {
                let uploaded = try await api.upload(send.attachments, sessionID: sessionID)
                updateLocalSend(send.id, in: sessionID) { $0.uploaded = uploaded }
                let receipt = try await api.sendInstruction(action: "send", sessionID: sessionID, message: send.text, turnSettings: send.turnSettings, busyDelivery: send.busyDelivery, author: "person", attachments: uploaded.map(\.id), idempotencyKey: send.idempotencyKey)
                if !send.turnSettings.isEmpty, turnSettingsDrafts[sessionID] == send.turnSettings { turnSettingsDrafts.removeValue(forKey: sessionID) }
                updateLocalSend(send.id, in: sessionID) { $0.status = LocalSend.label(for: receipt.result?.status, evidence: receipt.result?.evidence, waitingOnYou: receipt.result?.waitingOn != nil) }
                operationError = nil
                completion?(true)
            } catch {
                let failure = AgenthailAPIError.userMessage(error)
                updateLocalSend(send.id, in: sessionID) { $0.failure = failure }
                completion?(false)
            }
        }
    }

    func discardSend(_ send: LocalSend, in sessionID: String) {
        localSends[sessionID]?.removeAll { $0.id == send.id }
    }

    @Published private(set) var modelCatalog: [String: [ModelOption]] = [:]
    private var modelCatalogLoads: Set<String> = []

    func loadModelCatalog(for session: SessionState) async {
        let key = session.modelCatalogKey
        guard modelCatalog[key] == nil, !modelCatalogLoads.contains(key), let api else { return }
        modelCatalogLoads.insert(key)
        defer { modelCatalogLoads.remove(key) }
        if let options = try? await api.creationModels(surface: session.surface, host: session.remoteHostID) { modelCatalog[key] = options }
    }

    func setArchived(sessionID: String, _ archived: Bool) {
        perform(action: archived ? "session-archive" : "session-unarchive", sessionID: sessionID)
    }

    func setProjectArchived(_ projectID: String, _ archived: Bool) {
        projectAction(archived ? "project-archive" : "project-unarchive", projectID: projectID)
    }

    func addProject(path: String) {
        projectAction("project-add", path: path)
    }

    private func projectAction(_ action: String, projectID: String? = nil, path: String? = nil) {
        guard let api else { return }
        Task {
            do {
                _ = try await api.projectAction(action, projectID: projectID, path: path)
                operationError = nil
            } catch {
                operationError = AgenthailAPIError.userMessage(error)
            }
            _ = await refresh(fresh: true)
        }
    }

    func rename(_ sessionID: String, to alias: String) async -> String? {
        guard let api else { return "Agenthail isn't connected." }
        do {
            try await api.nameSession(id: sessionID, alias: alias)
        } catch {
            return AgenthailAPIError.userMessage(error)
        }
        _ = await refresh(fresh: true)
        return nil
    }

    var busyDelivery: FollowUpAction { FollowUpAction(rawValue: snapshot?.busyDelivery ?? "") ?? .queue }

    func setBusyDelivery(_ mode: FollowUpAction) {
        guard let snapshot, mode != busyDelivery else { return }
        updateDashboardConfig(busyDelivery: mode.rawValue, codexRecentHours: snapshot.codexRecentHours)
    }

    func setCodexRecentHours(_ hours: Int) {
        guard let snapshot, hours != snapshot.codexRecentHours else { return }
        updateDashboardConfig(busyDelivery: busyDelivery.rawValue, codexRecentHours: hours)
    }

    private func updateDashboardConfig(busyDelivery: String, codexRecentHours: Int) {
        guard let api else { return }
        Task {
            do {
                try await api.updateDashboardConfig(busyDelivery: busyDelivery, codexRecentHours: codexRecentHours)
                settings = try await api.settings()
                operationError = nil
            } catch {
                operationError = AgenthailAPIError.userMessage(error)
            }
            _ = await refresh(fresh: true)
        }
    }

    func repairSurface(_ surface: SurfaceState) async {
        guard let api, let action = surface.repairAction else { return }
        do {
            try await api.action(action)
            operationError = nil
        } catch {
            operationError = AgenthailAPIError.userMessage(error)
        }
        _ = await refresh(fresh: true)
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
                operationError = AgenthailAPIError.userMessage(error)
                return
            }
            do {
                let receipt = try await api.sendInstruction(action: "steer", sessionID: item.sessionId, message: item.message)
                localSends[item.sessionId, default: []].append(LocalSend(text: item.message, sentAt: Date(), status: LocalSend.label(for: receipt.result?.status, evidence: receipt.result?.evidence)))
                operationError = nil
            } catch {
                restoreToComposer(item.message, sessionID: item.sessionId)
                operationError = "Steer may not have reached the agent. The message is back in the composer; check the conversation before resending. \(AgenthailAPIError.userMessage(error))"
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
                operationError = AgenthailAPIError.userMessage(error)
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
                operationError = AgenthailAPIError.userMessage(error)
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

    private func restoreToComposer(_ text: String, attachments: [ComposerAttachment] = [], sessionID: String) {
        draft(for: sessionID).restore(text, attachments: attachments)
    }

    private func updateLocalSend(_ id: UUID, in sessionID: String, _ change: (inout LocalSend) -> Void) {
        let sessionID = rekeys.resolve(sessionID)
        guard let index = localSends[sessionID]?.firstIndex(where: { $0.id == id }) else { return }
        change(&localSends[sessionID]![index])
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
            fetchedSubagents.sessionRemoved(id, unlisted: event.data.reason == "unlisted")
            if let next = event.data.rekeyedTo { rekeys.record([id: next]) }
        case "delivery.problem":
            guard let deliveryID = event.data.deliveryId, let sessionID = event.data.sessionId else { return }
            let problem = DeliveryProblem(deliveryId: deliveryID, sessionId: sessionID, sourceSessionId: event.data.sourceSessionId, attentionSessionId: event.data.attentionSessionId, message: event.data.message ?? "", reason: event.data.reason ?? "", status: event.data.status, at: event.data.at ?? "")
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
            // The daemon derives the repair from the runtime problem, and health events carry no repair.
            let repairs = event.data.runtime?.problem == previous.runtime?.problem
            current.surfaces[index] = SurfaceState(name: name, connected: health == "healthy", error: health == "healthy" ? nil : event.data.detail, health: health, healthDetail: event.data.detail, capabilities: previous.capabilities, runtime: event.data.runtime, repairAction: repairs ? previous.repairAction : nil, repairLabel: repairs ? previous.repairLabel : nil)
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
    }

    func resolveSessionID(_ id: String) -> String {
        rekeys.resolve(id)
    }

    private func reconcilePanes() {
        if !rekeys.isEmpty {
            _ = rekeys.carry(&drafts)
            var sends = localSends
            if rekeys.carry(&sends) { localSends = sends }
            var settings = turnSettingsDrafts
            if rekeys.carry(&settings) { turnSettingsDrafts = settings }
        }
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
        connectionError = AgenthailAPIError.userMessage(error)
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
enum FollowUpAction: String {
    case queue
    case steer
}

final class ComposerDraft: ObservableObject {
    @Published var text = ""
    @Published var attachments: [ComposerAttachment] = []

    var isEmpty: Bool { ComposerAttachments.isEmpty(text: text, attachments: attachments) }

    func restore(_ restored: String, attachments restoredAttachments: [ComposerAttachment] = []) {
        if !restored.isEmpty {
            text = text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? restored : "\(text)\n\n\(restored)"
        }
        attachments = ComposerAttachments.adding(restoredAttachments, to: attachments).attachments
    }
}
