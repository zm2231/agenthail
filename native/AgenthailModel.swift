import AppKit
import Foundation

@MainActor
final class AgenthailModel: ObservableObject {
    @Published var snapshot: DashboardSnapshot? {
        didSet {
            rederive()
            trackFinishedSessions(from: oldValue)
            pruneStore()
            if let snapshot {
                HolderPins.shared.prune(to: snapshot.sessions)
                let picks = HolderPins.shared.all
                if holderPicks != picks { holderPicks = picks }
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
    @Published var paletteVisible = false
    @Published var searchQuery = ""
    @Published private(set) var searchResults: [SessionSearchItem] = []
    @Published private(set) var searching = false
    @Published private(set) var searchError: String?
    @Published private(set) var pinnedSessions: [SessionState] = [] {
        didSet { rederive() }
    }
    private var searchTask: Task<Void, Never>?
    @Published var devices: [DeviceState] = []
    @Published var signin: SigninState?
    @Published var permissions: PermissionsState?
    @Published var permissionsError: String?
    @Published var signinError: String?
    @Published var pairing: PairingResponse?
    @Published var settings: DashboardSettingsState?
    @Published var claudeRemoteControl: ClaudeRemoteControlState?
    @Published var audit: [HistoryState] = []
    @Published var auditKinds: [String] = []
    @Published var auditHasMore = false
    @Published var auditQuery = ""
    @Published var auditKind = ""
    @Published var connectionError: String?
    /// The build of the daemon on this Mac, as it last named it.
    @Published private(set) var daemonBuild: AgenthailBuild?
    /// Set when that daemon is another build than this app.
    var daemonBuildNotice: String? { AgenthailBuild.daemonNotice(app: .app(Bundle.main.infoDictionary), daemon: daemonBuild) }
    @Published private(set) var daemonSlow = false
    @Published var reconnecting = false
    @Published var operationError: String?
    @Published var pendingForks: Set<String> = []
    @Published var loading = false
    @Published var operationsVisible = false
    @Published var sessionFilter: SessionFilter = .recent {
        didSet { rederive() }
    }
    @Published var sessionRefinement = SessionRefinement() {
        didSet { rederive() }
    }
    @Published private(set) var localSends: [String: [LocalSend]] = [:]
    @Published private var turnSettingsDrafts: [String: TurnSettings] = [:]
    @Published private(set) var fetchedSubagents = SubagentPageCache()

    private(set) var api: AgenthailAPI?
    let interface = InterfaceStore()
    let actionKeys = ActionRetryKeys()
    private(set) var mainPane: SessionPane!
    weak var mainWindow: NSWindow?
    var onSnapshotRequest: ((SnapshotRequest, AgenthailAPI) -> Void)?
    var onSnapshotClosed: ((String) -> Void)?
    private var panes: [SessionPane] = []
    private var visibleWindows = 0
    private var connectionTask: Task<Void, Never>?
    private var eventTask: Task<Void, Never>?
    private var refreshTask: Task<Void, Never>?
    private var statusRefreshTask: Task<Void, Never>?
    private var lastEventID: UInt64 = 0
    private var catalogStreamTask: Task<Void, Never>?
    private var catalogPosition = CatalogPosition(epoch: nil, cursor: 0)
    let store = SessionStore(limits: .desktop)
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
    private(set) var knownSessions: [SessionState] = []
    var deliveryProblems: [DeliveryProblem] { snapshot?.deliveryProblems ?? [] }
    private(set) var sessionFamilies: [SessionFamily] = []
    private(set) var sessionAttention: [String: SessionAttention] = [:]
    private(set) var sessionTree = SessionTree.build([], filter: .recent, attention: [:])

    /// Rebuilds the sidebar's sessions, attention and tree when one of their
    /// inputs changes, so views read them instead of rebuilding them.
    private func rederive() {
        let listed = snapshot?.sessions ?? []
        let listedIDs = Set(listed.map(\.id))
        knownSessions = listed + pinnedSessions.filter { !listedIDs.contains($0.id) }
        sessionFamilies = SessionFamilies.build(knownSessions)
        sessionAttention = SessionAttention.build(sessions: knownSessions, attention: snapshot?.attention ?? [], deliveryProblems: deliveryProblems)
        sessionTree = SessionTree.build(knownSessions, families: sessionFamilies, filter: sessionFilter, refinement: sessionRefinement, attention: sessionAttention)
    }

    init(connecting: Bool = true, api: AgenthailAPI? = nil) {
        self.api = api
        mainPane = SessionPane(model: self, restoresSelection: true)
        panes = [mainPane]
        store.pinned = { [unowned self] id in
            panes.contains { $0.selectedSessionID == id } || drafts[id]?.isEmpty == false
        }
        store.working = { [unowned self] id in knownSessions.first { $0.id == id }?.isWorking == true }
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
        let picks = HolderPins.shared.all
        if holderPicks != picks { holderPicks = picks }
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
                    daemonBuild = try await api.version().build
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
            adopt(loaded.keepingCatalog(of: snapshot, appliedThrough: catalogPosition.cursor))
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

    func detailLoaded(_ loaded: WarmSession, for id: String) {
        store.keep(loaded, for: id)
        if let items = loaded.detail.timeline?.items, let sends = localSends[id] {
            localSends[id] = LocalSend.reconcile(sends, with: items)
        }
    }

    func openNewSession(message: String? = nil) {
        paletteVisible = false
        mainPane.openNewSession(message: message)
    }

    func receiveSharedText(_ text: String) {
        if let session = mainPane.selectedSession, mainPane.removedSession == nil, !session.isReadOnly, session.capabilities.send {
            draft(for: session.id).restore(text)
        } else {
            openNewSession(message: text)
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

    private func pruneStore() {
        guard snapshot != nil else { return }
        store.carry(rekeys)
        store.prune(keeping: Set(knownSessions.map(\.id)))
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

    func creationRootUsage(root: String, host: String?) async throws -> ClaudeRootUsage {
        guard let api else { throw AgenthailAPIError.unavailable("Agenthail isn't connected.") }
        return try await api.claudeRootUsage(root: root, host: host)
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
                await openCreatedSession(id, started: true)
                return .opened
            case .submitted(let note):
                operationError = nil
                return .submitted(note)
            case .starting:
                return .starting("Agenthail is still starting this session. It appears in the sidebar once it starts.")
            case .unconfirmed(let id):
                guard let id else { return .unconfirmed(SessionCreationReceipt.unknownOutcomeMessage) }
                await openCreatedSession(id, started: true)
                return .unconfirmed("This session may still be starting. It will appear in the sidebar when ready.")
            case .halted(let message):
                return .halted(message)
            case .failed(let message):
                return .failed(message)
            }
        } catch AgenthailAPIError.outcomeUnknown {
            return .unconfirmed(SessionCreationReceipt.unknownOutcomeMessage)
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

    func openCreatedSession(_ id: String, in pane: SessionPane? = nil, started: Bool = false) async {
        await refresh(fresh: true)
        if !knownSessions.contains(where: { AgenthailLink.matches($0, reference: id) }), let api, let detail = try? await api.sessionDetail(id: id) {
            var created = SessionState(id: detail.session.id, surface: detail.session.surface, name: detail.session.name, alias: detail.alias.flatMap { $0.isEmpty ? nil : $0 }, status: detail.session.status, lastActive: detail.session.lastActive, queueCount: 0, open: true, current: false, currentReason: nil, capabilities: detail.capabilities, readOnly: detail.readOnly, readOnlyReason: detail.readOnlyReason, cwd: detail.session.cwd)
            created.host = snapshot?.daemon.pairedHost(owning: detail.session.id)
            pin(created)
        }
        guard let session = knownSessions.first(where: { AgenthailLink.matches($0, reference: id) }) else { return }
        if started {
            mainPane.showStarted(session.id)
        } else {
            (pane ?? mainPane).select(session.id, keep: true)
        }
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

    /// Stops a session and resumes it, which clears a prompt nothing shows.
    func stopAndResume(_ sessionID: String) {
        Task {
            guard await performNow(action: "session-lifecycle-stop", sessionID: sessionID) else { return }
            await performNow(action: "session-lifecycle-resume", sessionID: sessionID)
        }
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

    /// Sets the name this host's sessions call the person; empty goes back
    /// to the account's name.
    func setPersonName(_ name: String) {
        guard let api else { return }
        Task {
            do {
                try await api.setPersonName(name.trimmingCharacters(in: .whitespacesAndNewlines))
                operationError = nil
                _ = await refresh(fresh: true)
            } catch {
                operationError = AgenthailAPIError.userMessage(error)
            }
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
        claudeRemoteControl = try? await api.claudeRemoteControl()
    }

    func setClaudeRemoteControl(root: String, atStartup: Bool) {
        guard let api else { return }
        Task {
            do {
                claudeRemoteControl = try await api.setClaudeRemoteControl(root: root, atStartup: atStartup)
                operationError = nil
            } catch {
                operationError = AgenthailAPIError.userMessage(error)
            }
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

    func performOperation(action: String, message: String? = nil, channel: String? = nil, targetID: String? = nil, fromID: String? = nil, toID: String? = nil, pattern: String? = nil, once: Bool? = nil, handoff: String? = nil, relayID: Int64? = nil, author: String? = nil) {
        guard let api else { return }
        Task {
            do {
                try await api.action(action, message: message, channel: channel, targetID: targetID, fromID: fromID, toID: toID, pattern: pattern, once: once, handoff: handoff, relayID: relayID, author: author)
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

    func createLoginLink() async -> String? {
        guard let api else { return nil }
        do {
            let link = try await api.createLoginLink(remote: true)
            operationError = nil
            return link.url
        } catch {
            operationError = AgenthailAPIError.userMessage(error)
            return nil
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

    var projects: [ProjectState] { snapshot?.projects ?? [] }

    func addProject(path: String, title: String? = nil, host: String? = nil) async -> ProjectChange {
        await projectChange {
            let (project, created) = try await $0.addProject(path: path, title: title, host: host)
            let name = project?.displayName ?? "This folder"
            return created ? "Added project \(name)." : "\(name) is already a project."
        }
    }

    func renameProject(_ projectID: String, to title: String) async -> ProjectChange {
        await projectChange {
            try await $0.renameProject(projectID, title: title)
            return "Renamed to \(title)."
        }
    }

    func removeProject(_ projectID: String) async -> ProjectChange {
        await projectChange {
            try await $0.projectAction("project-remove", projectID: projectID)
            return "Project removed."
        }
    }

    func clearLead(of project: ProjectState) async -> ProjectChange {
        await projectChange {
            try await $0.clearLead(projectID: project.id)
            return "\(project.displayName) has no lead now."
        }
    }

    func makeLead(_ sessionID: String, replace: Bool = false) async -> ProjectChange {
        await projectChange {
            let lead = try await $0.makeLead(sessionID: sessionID, replace: replace)
            return "@\(lead.name) leads \(lead.projectRoot)."
        }
    }

    func startLead(of project: ProjectState, in checkout: ProjectState.Checkout, surface: String, name: String, replace: Bool = false) async -> ProjectChange {
        await projectChange {
            let lead = try await $0.startLead(surface: surface, folder: checkout.rootPath, host: ProjectMenu.host(of: checkout), name: name, replace: replace)
            return "@\(lead.name) leads \(project.displayName); its first message starts it."
        }
    }

    private func projectChange(_ body: (AgenthailAPI) async throws -> String) async -> ProjectChange {
        guard let api else { return .failed(AgenthailAPIError.disconnectedGuidance) }
        let change = await ProjectChange.run { try await body(api) }
        _ = await refresh(fresh: true)
        return change
    }

    private func projectAction(_ action: String, projectID: String) {
        guard let api else { return }
        Task {
            do {
                try await api.projectAction(action, projectID: projectID)
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

    /// Whether this computer resumes sessions a usage limit stopped; on unless turned off.
    var resumesAfterLimit: Bool { settings?.dashboard.resumeAfterLimit != false }

    func setResumeAfterLimit(_ enabled: Bool) {
        guard let api else { return }
        Task {
            do {
                try await api.setResumeAfterLimit(enabled)
                settings = try await api.settings()
                operationError = nil
            } catch {
                operationError = AgenthailAPIError.userMessage(error)
            }
        }
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
                let receipt = try await api.sendInstruction(action: "steer", sessionID: item.sessionId, message: item.message, author: "person")
                localSends[item.sessionId, default: []].append(LocalSend(text: item.message, sentAt: Date(), status: LocalSend.label(for: receipt.result?.status, evidence: receipt.result?.evidence)))
                operationError = nil
            } catch {
                restoreToComposer(item.message, sessionID: item.sessionId)
                operationError = "Steer may not have reached the agent. The message is back in the composer; check the conversation before resending. \(AgenthailAPIError.userMessage(error))"
            }
            _ = await refresh(fresh: true)
        }
    }

    func removeQueued(_ item: QueueState) {
        guard let api else { return }
        Task {
            do {
                try await api.action("queue-cancel", queueID: item.id)
                operationError = nil
            } catch {
                operationError = AgenthailAPIError.userMessage(error)
            }
            _ = await refresh(fresh: true)
        }
    }

    /// Cancels queued messages oldest first and puts each one that was still
    /// waiting back ahead of the draft; a message already on its way stays queued.
    func takeBackQueued(_ items: [QueueState], sessionID: String) {
        guard let api else { return }
        Task {
            var taken: [String] = []
            operationError = nil
            for item in items.sorted(by: { $0.id < $1.id }) {
                do {
                    try await api.action("queue-cancel", queueID: item.id)
                    taken.append(item.message)
                } catch {
                    operationError = AgenthailAPIError.userMessage(error)
                    break
                }
            }
            if !taken.isEmpty { draft(for: sessionID).takeBack(taken) }
            _ = await refresh(fresh: true)
        }
    }

    func deliveryProblems(for sessionID: String) -> [DeliveryProblem] {
        deliveryProblems.filter { $0.sessionId == sessionID || $0.sourceSessionId == sessionID }
    }

    func sessionTitle(for sessionID: String) -> String {
        knownSessions.first { $0.id == sessionID }?.title ?? "Another session"
    }

    func loadPrompts(for sessionID: String) async {
        guard let api, let page = try? await api.prompts(sessionID: sessionID, limit: 100) else { return }
        draft(for: sessionID).recordSent(page.prompts)
    }

    /// Searches what was typed into one session or, without one, into every
    /// session on this computer and each connected paired host. A host that
    /// cannot answer is reported by name and leaves the others' results.
    func searchPrompts(_ query: String, sessionID: String?) async -> (prompts: [PromptItem], problems: [String]) {
        guard let api else { return ([], ["Agenthail isn't connected."]) }
        if let sessionID {
            do {
                return (try await api.prompts(sessionID: sessionID, query: query, limit: 100).prompts, [])
            } catch {
                return ([], [AgenthailAPIError.userMessage(error)])
            }
        }
        let hosts = (snapshot?.daemon.hosts ?? []).filter { $0.state == "connected" }
        let results = await withTaskGroup(of: (prompts: [PromptItem], problem: String?).self) { group in
            group.addTask {
                do { return (try await api.prompts(query: query, limit: 100).prompts, nil) } catch { return ([], AgenthailAPIError.userMessage(error)) }
            }
            for host in hosts {
                group.addTask {
                    do {
                        let prompts = try await api.prompts(query: query, limit: 100, host: host.id).prompts.map { prompt in
                            var prompt = prompt
                            prompt.sessionId = host.id + "::" + prompt.sessionId
                            return prompt
                        }
                        return (prompts, nil)
                    } catch {
                        return ([], "\(host.name): \(AgenthailAPIError.userMessage(error))")
                    }
                }
            }
            var collected: [(prompts: [PromptItem], problem: String?)] = []
            for await result in group { collected.append(result) }
            return collected
        }
        let prompts = results.flatMap(\.prompts).sorted { ($0.sentAt ?? .distantPast) > ($1.sentAt ?? .distantPast) }
        return (prompts, results.compactMap(\.problem))
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
                        let build = try await reloadedAPI.version().build
                        if Task.isCancelled { return }
                        daemonBuild = build
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
        if event.type == "ui.changed" {
            lastEventID = max(lastEventID, event.id)
            if let api { interface.load(from: api) }
            return
        }
        if event.type == "ui.snapshot.requested" {
            lastEventID = max(lastEventID, event.id)
            if let request = event.snapshot, let api {
                onSnapshotRequest?(request, api)
            }
            return
        }
        if event.type == "ui.snapshot.closed" {
            lastEventID = max(lastEventID, event.id)
            if let id = event.entityId { onSnapshotClosed?(id) }
            return
        }
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
        if let api { interface.load(from: api) }
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
    @Published private(set) var stash: ComposerStash?
    @Published private(set) var sent: [PromptItem] = []
    private(set) var cleared: [String] = []
    private var lastEscape: Date?
    var history = PromptHistory()

    var isEmpty: Bool { ComposerAttachments.isEmpty(text: text, attachments: attachments) }

    /// Stashes the draft, or brings the stash back into an empty composer.
    /// Stashing while a stash is held swaps the two, so neither is lost.
    @discardableResult
    func toggleStash() -> Bool {
        if !isEmpty {
            let current = ComposerStash(text: text, attachments: attachments)
            text = stash?.text ?? ""
            attachments = stash?.attachments ?? []
            stash = current
            return true
        }
        guard let held = stash else { return false }
        text = held.text
        attachments = held.attachments
        stash = nil
        return true
    }

    func restoreStashAfterSend() {
        guard isEmpty, let held = stash else { return }
        text = held.text
        attachments = held.attachments
        stash = nil
    }

    /// True on the second Esc within the system double-click interval.
    func escapePressed(at now: Date = Date(), within interval: TimeInterval = NSEvent.doubleClickInterval) -> Bool {
        if let lastEscape, now.timeIntervalSince(lastEscape) < interval {
            self.lastEscape = nil
            return true
        }
        lastEscape = now
        return false
    }

    func escapeHandled() { lastEscape = nil }

    @discardableResult
    func clearIntoHistory() -> Bool {
        guard !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return false }
        cleared.insert(text, at: 0)
        text = ""
        history.stopBrowsing()
        rebuildHistory()
        return true
    }

    /// Takes the host's record of what was sent, keeping prompts sent from
    /// here that the host has not recorded yet.
    func recordSent(_ prompts: [PromptItem]) {
        let recorded = Set(prompts.map(\.text))
        sent = sent.filter { $0.sessionId.isEmpty && !recorded.contains($0.text) } + prompts
        rebuildHistory()
    }

    func recordSending(_ text: String) {
        history.stopBrowsing()
        cleared.removeAll { $0 == text }
        guard !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
        sent.insert(PromptItem(sessionId: "", itemId: UUID().uuidString, turnId: nil, ts: "", text: text), at: 0)
        rebuildHistory()
    }

    private func rebuildHistory() {
        history.replace(cleared + sent.map(\.text))
    }

    func restore(_ restored: String, attachments restoredAttachments: [ComposerAttachment] = []) {
        if !restored.isEmpty {
            text = text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? restored : "\(text)\n\n\(restored)"
        }
        attachments = ComposerAttachments.adding(restoredAttachments, to: attachments).attachments
    }

    func takeBack(_ queued: [String]) {
        text = QueueTakeBack.text(queued: queued, draft: text)
    }
}
