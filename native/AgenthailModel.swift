import AppKit
import Foundation
import Observation
import os

@MainActor
@Observable
final class AgenthailModel {
    var snapshot: DashboardSnapshot? {
        didSet {
            adoptParts()
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
    private(set) var finishedUnseen: Set<String> = []
    private(set) var holderPicks: [String: HolderPin] = [:]
    private(set) var snapshotLoadedAt: Date?
    var paletteVisible = false
    var searchQuery = ""
    private(set) var searchResults: [SessionSearchItem] = []
    private(set) var searching = false
    private(set) var searchError: String?
    private(set) var pinnedSessions: [SessionState] = [] {
        didSet { rederive() }
    }
    private var searchTask: Task<Void, Never>?
    var devices: [DeviceState] = []
    var signin: SigninState?
    var permissions: PermissionsState?
    var permissionsError: String?
    var signinError: String?
    var pairing: PairingResponse?
    var settings: DashboardSettingsState?
    var claudeRemoteControl: ClaudeRemoteControlState?
    var audit: [HistoryState] = []
    var auditKinds: [String] = []
    var auditHasMore = false
    var auditQuery = ""
    var auditKind = ""
    var connectionError: String?
    /// The build of the daemon on this Mac, as it last named it.
    private(set) var daemonBuild: AgenthailBuild?
    /// Set when that daemon is another build than this app.
    var daemonBuildNotice: String? { AgenthailBuild.daemonNotice(app: .app(Bundle.main.infoDictionary), daemon: daemonBuild) }
    private(set) var daemonSlow = false
    var reconnecting = false
    var grace = ConnectionGrace()
    private var streamOpen = false
    private(set) var heldByGrace = false
    private static let connectionLog = Logger(subsystem: "com.agenthail.app", category: "connection")
    var operationError: String?
    var pendingForks: Set<String> = []
    var loading = false
    var operationsVisible = false
    var sessionFilter: SessionFilter = .recent {
        didSet {
            orderHold.release()
            listChanged()
            rederive()
        }
    }
    var sessionRefinement = SessionRefinement() {
        didSet {
            orderHold.release()
            listChanged()
            rederive()
        }
    }
    @ObservationIgnored private(set) lazy var reader = makeReader()
    @ObservationIgnored let scopeTrees = ScopeTreeCache()
    var pages: ScopePages { reader.pages }
    private(set) var currentView: ScopePage.View? {
        didSet {
            guard oldValue != currentView else { return }
            rederive()
            scheduleListReread()
        }
    }
    private(set) var currentViewPosition = CatalogPosition(epoch: nil, cursor: 0)
    private var orderReread: Task<Void, Never>?
    private var orderRereadDirty = false
    var listRereadTask: Task<Void, Never>?
    var findTask: Task<Void, Never>?
    var spotlightCatalog: SpotlightCatalog?
    private(set) var localSends: [String: [LocalSend]] = [:]
    private(set) var fetchedSubagents = SubagentPageCache()

    private(set) var api: AgenthailAPI?
    let interface = InterfaceStore()
    let actionKeys = ActionRetryKeys()
    private(set) var mainPane: SessionPane!
    @ObservationIgnored weak var mainWindow: NSWindow?
    @ObservationIgnored weak var mainBackdrop: WindowBackdrop?
    var onSnapshotRequest: ((SnapshotRequest, AgenthailAPI) -> Void)?
    var onSnapshotClosed: ((String) -> Void)?
    private var panes: [SessionPane] = []
    private var visibleWindows = 0
    @ObservationIgnored private var connectionTask: Task<Void, Never>?
    @ObservationIgnored private var eventTask: Task<Void, Never>?
    @ObservationIgnored private var refreshTask: Task<Void, Never>?
    @ObservationIgnored private(set) var pendingRefresh = EventRefresh()
    @ObservationIgnored private var statusRefreshTask: Task<Void, Never>?
    @ObservationIgnored private(set) var lastEventID: UInt64 = 0
    private(set) var catalogPosition = CatalogPosition(epoch: nil, cursor: 0)
    @ObservationIgnored lazy var catalogFeed = makeCatalogFeed()
    @ObservationIgnored lazy var decisionFeed = DecisionFeed(key: { [unowned self] in decisionsKey }, load: { [unowned self] in await loadDecisions() })
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

    private(set) var hasSnapshot = false
    private(set) var daemon: DaemonState?
    private(set) var attention: [AttentionState] = []
    private(set) var queue: [QueueState] = []
    private(set) var history: [HistoryState] = []
    private(set) var deliveryProblems: [DeliveryProblem] = []
    private(set) var leadReplacements: [LeadReplacement] = []
    private(set) var projects: [ProjectState] = []
    private(set) var codexRecentHours = 5
    private(set) var busyDelivery = FollowUpAction.queue

    private func adoptParts() {
        hasSnapshot = snapshot != nil
        daemon = snapshot?.daemon
        attention = snapshot?.attention ?? []
        queue = snapshot?.queue ?? []
        history = snapshot?.history ?? []
        deliveryProblems = snapshot?.deliveryProblems ?? []
        leadReplacements = snapshot?.leadReplacements ?? []
        projects = snapshot?.projects ?? []
        codexRecentHours = snapshot?.codexRecentHours ?? 5
        busyDelivery = FollowUpAction(rawValue: snapshot?.busyDelivery ?? "") ?? .queue
    }

    var isConnected: Bool { connectionError == nil && daemon?.running == true }
    var currentSessions: [SessionState] { snapshot?.sessions.filter(\.current) ?? [] }
    private(set) var knownSessions: [SessionState] = []
    let cells = SessionCells()
    private(set) var workingCount = 0

    func session(_ id: String?) -> SessionState? { cells.session(id) }
    func title(of session: SessionState) -> String { cells.title(of: session) }

    func sessionName(_ id: String) -> String {
        cells.session(id)?.displayName ?? id
    }
    private(set) var sessionAttention: [String: SessionAttention] = [:]
    private(set) var sessionTree = SessionTree.empty
    /// Kept apart from the tree so window chrome isn't redrawn by every row change.
    private(set) var needsYou: [SessionState] = []
    private var orderHold = OrderHold()

    /// Rebuilds the sidebar's sessions, attention and tree when one of their
    /// inputs changes, so views read them instead of rebuilding them.
    func rederive() {
        let listed = reader.joined(snapshot?.sessions ?? [])
        let listedIDs = Set(listed.map(\.id))
        knownSessions = listed + pinnedSessions.filter { !listedIDs.contains($0.id) }
        cells.adopt(knownSessions)
        workingCount = knownSessions.filter { $0.archived != true && SessionDisplayKind.of($0) == .working }.count
        sessionAttention = SessionAttention.build(sessions: knownSessions, attention: snapshot?.attention ?? [], deliveryProblems: deliveryProblems)
        let rows = Dictionary(knownSessions.map { ($0.id, $0) }, uniquingKeysWith: { first, _ in first })
        let shown = orderHold.shown(list: listView, current: currentView, rows: rows)
        sessionTree = SessionTree.ordered(list: shown.list, current: shown.current, rows: shown.rows, attention: sessionAttention)
        needsYou = sessionTree.needsYou
    }

    /// Keeps the sidebar's order while the person aims at it; rows still update in place.
    func holdOrder(_ holding: Bool) {
        guard holding != orderHold.held else { return }
        if holding {
            orderHold.hold(list: listView, current: currentView, rows: Dictionary(knownSessions.map { ($0.id, $0) }, uniquingKeysWith: { first, _ in first }))
        } else {
            orderHold.release()
            rederive()
        }
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
                        catalogFeed.start()
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
            async let parts = api.snapshotBaseAndCurrent(fresh: fresh)
            async let activity = api.projectActivity(fresh: fresh)
            async let history = api.history(limit: 50)
            let ((readBase, readCurrent), readActivity, readHistory) = try await (parts, activity, history)
            let loaded = DashboardSnapshot(base: readBase, current: readCurrent, activity: readActivity, history: readHistory.items)
            adoptRefreshed(loaded)
            adoptCurrentView(readCurrent)
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

    /// Takes a Current view unless one from a later catalog position is already shown.
    func adoptCurrentView(_ read: ScopeSessions, mergingRows: Bool = false) {
        guard let view = read.view else { return }
        let seq = read.catalogSeq ?? 0
        if let epoch = catalogPosition.epoch, read.hostEpoch != epoch { return }
        if read.hostEpoch == currentViewPosition.epoch, seq < currentViewPosition.cursor { return }
        currentViewPosition = CatalogPosition(epoch: read.hostEpoch, cursor: seq)
        if mergingRows, var merged = snapshot {
            let held = Set(merged.sessions.map(\.id))
            merged.sessions += read.sessions.filter { !held.contains($0.id) }
            if merged.sessions.count != snapshot?.sessions.count { snapshot = merged }
        }
        currentView = view
    }

    /// Rereads the order after news: one request in flight, and one more if news arrived meanwhile.
    func rereadOrder() {
        guard orderReread == nil else {
            orderRereadDirty = true
            return
        }
        orderReread = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(300))
            guard let self, !Task.isCancelled else { return }
            reader.rereadAll()
            if let api, let read = try? await api.scopeSessions("current") { adoptCurrentView(read, mergingRows: true) }
            orderReread = nil
            if orderRereadDirty {
                orderRereadDirty = false
                rereadOrder()
            }
        }
    }

    func adoptRefreshed(_ loaded: DashboardSnapshot) {
        let before = Set(snapshot?.sessions.map(\.id) ?? [])
        let hadEpoch = catalogPosition.epoch != nil
        adopt(loaded.keepingCatalog(of: snapshot, appliedThrough: catalogPosition.cursor))
        reader.refreshed(removedFromCurrent: !before.subtracting(snapshot?.sessions.map(\.id) ?? []).isEmpty)
        if !pages.isEmpty { rederive() }
        spotlightCatalog?.epoch(loaded.hostEpoch)
        if catalogPosition.adopt(snapshotEpoch: loaded.hostEpoch, snapshotSeq: loaded.catalogSeq) {
            if hadEpoch { reader.rereadAll() }
            if catalogFeed.isRunning { catalogFeed.start() }
        }
        if let request = listRequest, pages.listings[request] == nil { Task { await reader.readMore(request) } }
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

    func creationRootPick(host: String?) async throws -> ClaudeRootPick {
        guard let api else { throw AgenthailAPIError.unavailable("Agenthail isn't connected.") }
        return try await api.claudeRootPick(host: host)
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
        if !knownSessions.contains(where: { AgenthailLink.matches($0, reference: id) }), let api, let detail = try? await api.sessionDetail(id: id, waitForCatchUp: false) {
            pin(detail.sessionState(open: true, host: snapshot?.daemon.pairedHost(owning: detail.session.id)))
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
        findSessions(query)
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

    func pin(_ session: SessionState) {
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

    func setTurnSetting(_ key: String, to value: String, sessionID: String) async -> Bool {
        guard let api else { return false }
        do {
            try await api.setTurnSetting(sessionID: sessionID, key: key, value: value)
            operationError = nil
            return true
        } catch {
            operationError = AgenthailAPIError.userMessage(error)
            return false
        }
    }

    func moveToAccount(sessionID: String, root: String) async -> Bool {
        guard let api else { return false }
        do {
            try await api.moveToAccount(sessionID: sessionID, root: root)
            operationError = nil
            _ = await refresh(fresh: true)
            return true
        } catch {
            operationError = AgenthailAPIError.userMessage(error)
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
        let waiter = Task { @MainActor in await waitUntil { self.snapshot != nil } }
        let timer = Task {
            try? await Task.sleep(for: timeout)
            waiter.cancel()
        }
        let connected = await waiter.value
        timer.cancel()
        return connected && api != nil
    }

    func send(_ message: String, attachments: [ComposerAttachment] = [], to sessionID: String, busyDelivery: String? = nil, completion: ((Bool) -> Void)? = nil) {
        let text = message.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !ComposerAttachments.isEmpty(text: text, attachments: attachments), let api else {
            restoreToComposer(text, attachments: attachments, sessionID: sessionID)
            completion?(false)
            return
        }
        let pending = LocalSend(text: text, attachments: attachments, sentAt: Date(), status: nil, busyDelivery: busyDelivery)
        localSends[sessionID, default: []].append(pending)
        deliver(pending, to: sessionID, api: api, completion: completion)
    }

    func retrySend(_ send: LocalSend, in sessionID: String) {
        guard let api, let index = localSends[sessionID]?.firstIndex(where: { $0.id == send.id }) else { return }
        localSends[sessionID]?[index].failure = nil
        localSends[sessionID]?[index].deliveryID = nil
        localSends[sessionID]?[index].deletable = false
        deliver(send, to: sessionID, api: api)
    }

    private func deliver(_ send: LocalSend, to sessionID: String, api: AgenthailAPI, completion: ((Bool) -> Void)? = nil) {
        Task {
            do {
                let uploaded = try await api.upload(send.attachments, sessionID: sessionID)
                updateLocalSend(send.id, in: sessionID) { $0.uploaded = uploaded }
                let receipt = try await api.sendInstruction(action: "send", sessionID: sessionID, message: send.text, busyDelivery: send.busyDelivery, author: "person", attachments: uploaded.map(\.id), idempotencyKey: send.idempotencyKey)
                updateLocalSend(send.id, in: sessionID) { $0.status = LocalSend.label(for: receipt.result?.status, waitingOnYou: receipt.result?.waitingOn != nil, movedTo: receipt.result?.movedTo) }
                operationError = nil
                completion?(true)
            } catch {
                let failure = AgenthailAPIError.userMessage(error)
                let kept = error as? KeptDelivery
                updateLocalSend(send.id, in: sessionID) {
                    $0.failure = failure
                    $0.deliveryID = kept?.deliveryID
                    $0.deletable = kept?.deletable ?? false
                }
                completion?(false)
            }
        }
    }

    func discardSend(_ send: LocalSend, in sessionID: String) {
        localSends[sessionID]?.removeAll { $0.id == send.id }
    }

    func deleteSend(_ send: LocalSend, in sessionID: String) {
        guard send.canDelete else { return }
        let position = localSends[sessionID]?.firstIndex { $0.id == send.id }
        discardSend(send, in: sessionID)
        guard let deliveryID = send.deliveryID, let api else { return }
        Task {
            do {
                try await api.action("delivery-delete", deliveryID: deliveryID)
                operationError = nil
            } catch {
                operationError = AgenthailAPIError.userMessage(error)
                if let position, localSends[sessionID]?.contains(where: { $0.id == send.id }) != true {
                    localSends[sessionID, default: []].insert(send, at: min(position, localSends[sessionID]?.count ?? 0))
                }
            }
        }
    }

    private(set) var modelCatalog: [String: [ModelOption]] = [:]
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

    var claudeDefaultRoot: String { settings?.claudeDefaultRoot ?? "" }

    func setClaudeDefaultRoot(_ name: String) {
        guard let api, name != claudeDefaultRoot else { return }
        Task {
            do {
                try await api.setClaudeDefaultRoot(name)
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
        QueueSteer.waiting(queue.filter { $0.sessionId == sessionID })
    }

    func steerQueued(_ item: QueueState, capabilities: Capabilities) {
        guard let api, QueueSteer.refusal(item, capabilities: capabilities) == nil else { return }
        Task {
            let cancelled: CancelledQueueItem
            do {
                cancelled = try await api.cancelQueued(item)
            } catch {
                operationError = AgenthailAPIError.userMessage(error)
                return
            }
            let message = cancelled.message ?? item.message
            let attachments = cancelled.attachments ?? []
            do {
                let receipt = try await api.steerQueued(message: message, attachments: attachments, sessionID: item.sessionId)
                localSends[item.sessionId, default: []].append(LocalSend(text: message, attachments: attachments.map { ComposerAttachment(uploaded: $0.upload) }, sentAt: Date(), status: LocalSend.label(for: receipt.result?.status, steer: true)))
                operationError = nil
            } catch {
                let lost = draft(for: item.sessionId).takeBack([message], attachments: attachments)
                let problem = "Steer may not have reached the agent. The message is back in the composer; check the conversation before resending. \(AgenthailAPIError.userMessage(error))"
                operationError = [problem, ComposerAttachments.lostNotice(lost)].compactMap { $0 }.joined(separator: " ")
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
            var attachments: [QueueAttachment] = []
            operationError = nil
            for item in items.sorted(by: { $0.id < $1.id }) {
                do {
                    let cancelled = try await api.cancelQueued(item)
                    taken.append(cancelled.message ?? item.message)
                    attachments += cancelled.attachments ?? []
                } catch {
                    operationError = AgenthailAPIError.userMessage(error)
                    break
                }
            }
            if !taken.isEmpty {
                let lost = draft(for: sessionID).takeBack(taken, attachments: attachments)
                if let notice = ComposerAttachments.lostNotice(lost) {
                    operationError = [operationError, notice].compactMap { $0 }.joined(separator: " ")
                }
            }
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
        settleDeliveryProblem(problem, action: "delivery-dismiss")
    }

    func deleteDeliveryProblem(_ problem: DeliveryProblem) {
        settleDeliveryProblem(problem, action: "delivery-delete")
    }

    private func settleDeliveryProblem(_ problem: DeliveryProblem, action: String) {
        guard let api else { return }
        let position = snapshot?.deliveryProblems?.firstIndex { $0.deliveryId == problem.deliveryId }
        removeDeliveryProblem(problem.deliveryId)
        Task {
            do {
                try await api.action(action, deliveryID: problem.deliveryId)
                operationError = nil
            } catch {
                operationError = AgenthailAPIError.userMessage(error)
                if let position, snapshot?.deliveryProblems?.contains(where: { $0.deliveryId == problem.deliveryId }) == false {
                    snapshot?.deliveryProblems?.insert(problem, at: min(position, snapshot?.deliveryProblems?.count ?? 0))
                }
                _ = await refresh(fresh: true)
            }
        }
    }

    func acknowledgeLeadReplacement(_ replacement: LeadReplacement) {
        guard let api else { return }
        let position = snapshot?.leadReplacements?.firstIndex { $0.id == replacement.id }
        snapshot?.leadReplacements?.removeAll { $0.id == replacement.id }
        Task {
            do {
                try await api.acknowledgeLeadReplacement(replacement.id)
                operationError = nil
            } catch {
                operationError = AgenthailAPIError.userMessage(error)
                if let position, snapshot?.leadReplacements?.contains(where: { $0.id == replacement.id }) == false {
                    snapshot?.leadReplacements?.insert(replacement, at: min(position, snapshot?.leadReplacements?.count ?? 0))
                }
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

    private func makeCatalogFeed() -> CatalogFeed {
        CatalogFeed(CatalogFeed.Host(
            api: { [weak self] in self?.api },
            cursor: { [weak self] in self?.catalogPosition.cursor ?? 0 },
            refresh: { [weak self] in await self?.refresh(fresh: true) ?? false },
            resetCursor: { [weak self] in self?.catalogPosition = CatalogPosition(epoch: nil, cursor: 0) },
            apply: { [weak self] in self?.applyCatalogEvents($0) },
            stopped: { [weak self] in self == nil }
        ))
    }

    func applyCatalogEvents(_ events: [CatalogStreamEvent]) {
        guard let current = snapshot else { return }
        let fresh = events.filter { $0.stream == "catalog" && $0.seq > catalogPosition.cursor }
        let news = CatalogNews.moves(fresh, held: Dictionary(knownSessions.map { ($0.id, $0) }, uniquingKeysWith: { first, _ in first }))
        let batch = current.applyingCatalog(events, after: catalogPosition.cursor)
        catalogPosition.cursor = batch.cursor
        for removal in batch.removals {
            fetchedSubagents.sessionRemoved(removal.id, unlisted: removal.unlisted)
            if let next = removal.rekeyedTo { rekeys.record([removal.id: next]) }
            _ = reader.removed(removal.id, unlisted: removal.unlisted)
        }
        if batch.changed {
            snapshot = batch.snapshot
            if !pages.isEmpty {
                reader.adoptCurrent()
                rederive()
            }
            reconcilePanes()
        }
        if news { rereadOrder() }
        spotlightCatalog?.apply(upserted: batch.upserted, removed: batch.removals.map(\.id))
        if batch.needsSnapshot { Task { await refresh(fresh: true) } }
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
                    if streamOpen { grace.contact() }
                    streamOpen = false
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

    func receive(_ event: AgenthailEvent) {
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
        pendingRefresh.merge(EventRefresh(for: event.type, auditing: operationsVisible))
        guard !pendingRefresh.isEmpty else { return }
        refreshTask?.cancel()
        refreshTask = Task {
            try? await Task.sleep(for: .milliseconds(180))
            if Task.isCancelled { return }
            let due = pendingRefresh
            pendingRefresh = EventRefresh()
            if due.snapshot { _ = await refresh() }
            if due.operations {
                await loadOperations()
            } else if due.audit && operationsVisible {
                await loadAudit(reset: true)
            }
        }
    }

    private func eventStreamConnected() {
        if let api { interface.load(from: api) }
        reconnecting = false
        streamOpen = true
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
        }
        panes.forEach { $0.reconcile() }
    }

    private func setLoading(_ value: Bool) {
        if loading != value { loading = value }
    }

    private func clearConnectionError() {
        grace.contact()
        if connectionError != nil || heldByGrace {
            Self.connectionLog.notice("daemon reachable again")
            connectionError = nil
            heldByGrace = false
        }
        if daemonSlow { daemonSlow = false }
    }

    private func connectionFailed(_ error: Error) {
        if isConnected, grace.holds(error) {
            Self.connectionLog.notice("daemon request failed, still shown connected: \(error.localizedDescription, privacy: .public)")
            heldByGrace = true
            if !reconnecting { reconnecting = true }
            return
        }
        heldByGrace = false
        if isConnected { Self.connectionLog.notice("daemon shown unreachable: \(error.localizedDescription, privacy: .public)") }
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

    @discardableResult
    func takeBack(_ queued: [String], attachments queuedAttachments: [QueueAttachment] = []) -> [String] {
        text = QueueTakeBack.text(queued: queued, draft: text)
        let taken = ComposerAttachments.takingBack(queuedAttachments, to: attachments)
        attachments = taken.attachments
        return taken.lost
    }
}
