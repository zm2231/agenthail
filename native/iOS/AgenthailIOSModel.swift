import Foundation
import UIKit
import UserNotifications

struct PairedHostStatus: Identifiable, Equatable {
    let id: String
    let name: String
    let address: String
    let reachable: Bool
    let reconnecting: Bool
    let error: String?
    let surfaces: [SurfaceState]
    let signin: [SigninConsumerState]
    let peers: [HostHealth]
    let notifications: String
}

@MainActor
final class AgenthailIOSModel: ObservableObject, HostConnectionDelegate {
    static func shouldRefreshSnapshot(for eventType: String) -> Bool {
        eventType == "stream.reset" || eventType == "state.changed" || eventType == "settings.updated" || eventType.hasPrefix("device.")
    }

    static let voiceHostKey = "voiceHost"

    @Published private(set) var snapshot: DashboardSnapshot? {
        didSet {
            if let snapshot {
                HolderPins.shared.prune(to: snapshot.sessions)
                let picks = HolderPins.shared.all
                if holderPicks != picks { holderPicks = picks }
            }
        }
    }
    @Published private(set) var hosts: [PairedHostStatus] = []
    @Published private(set) var creationHosts: [CreationHost] = []
    @Published private(set) var holderPicks: [String: HolderPin] = [:]
    @Published var selectedDetail: SessionDetail?
    @Published var sessionRequests: SessionRequests?
    @Published var answeringRequest: String?
    private var sessionRequestsKey: String?
    @Published var selectedSessionID: String? {
        didSet { if selectedSessionID != oldValue { sessionServer = selectedSessionID.flatMap(server(for:)) } }
    }
    @Published private(set) var connectionError: String?
    @Published private(set) var reconnecting = false
    @Published var operationError: String?
    @Published var pairing = false
    @Published private(set) var completedPairings = 0
    let draft = ComposerDraft()
    var composer: String {
        get { draft.text }
        set { draft.text = newValue }
    }
    @Published var sessionError: String?
    @Published var loadingSession = false
    @Published var sendingSessionIDs: Set<String> = []
    private var attachmentCache: [String: Data] = [:]
    private var attachmentCacheBytes = 0
    private let attachmentCacheLimit = 20 * 1024 * 1024
    @Published var deliveryStatus: [String: String] = [:]
    @Published var controlOutput: [String: String] = [:]
    @Published var deliveryIDs: [String: Int64] = [:]
    private var deliveryQueueIDs: [String: Int64] = [:]
    private var refreshingDeliveries = false
    @Published var olderActivity: [TimelineItem] = []
    @Published var activityCursor: Int64?
    @Published var loadingOlderActivity = false
    @Published var olderActivityError: String?
    @Published var searchResults: [SessionSearchItem] = []
    @Published var searchError: String?
    @Published var searching = false
    private var searchQuery = ""
    private var sessionRequestID = UUID()
    private var drafts: [String: String] = [:]
    @Published var composerAttachments: [ComposerAttachment] = []
    @Published var attachmentProblem: String?
    private var attachmentDrafts: [String: [ComposerAttachment]] = [:]
    private var rekeys = SessionRekeys()
    private struct PendingSendRequest {
        let message: String
        let attachments: [ComposerAttachment]
        let turnSettings: TurnSettings
        let idempotencyKey: String
    }
    private struct CreationRequestIdentity: Equatable {
        let surface: String
        let message: String
        let cwd: String
        let model: String
        let turnSettings: TurnSettings
        let claudeFields: [String: String]
        let launcher: String?
        let host: String?
    }
    private struct StoredPush: Codable {
        let registration: PushRegistration
        let deviceToken: String
        let environment: String
        let endpoint: String
    }
    private var pendingSendRequests: [String: PendingSendRequest] = [:]
    private var creationRequestIdentity: CreationRequestIdentity?
    private var creationRequestKey: String?
    @Published var notificationStatus = "Not enabled"
    @Published var requestedSessionID: String?
    @Published var forgetCandidate: PairedHostStatus?
    @Published var showPairingConfirmation = false
    @Published var voiceHostID: String? {
        didSet { UserDefaults.standard.set(voiceHostID, forKey: Self.voiceHostKey) }
    }

    @Published var creatingSession = false
    @Published var creationError: String?
    @Published var creationWarning: String?
    @Published var pendingControls: Set<String> = []
    @Published private(set) var turnSettingsDrafts: [String: TurnSettings] = [:]
    @Published private(set) var fetchedSubagents = SubagentPageCache()

    private var connections: [String: HostConnection] = [:]
    private var hostOrder: [String] = []
    private var catalog = HostCatalog()
    private var sessionStreamTask: Task<Void, Never>?
    private var sessionMetadataTask: Task<Void, Never>?
    private var sessionServer: HostConnection?
    private(set) var sessionStreamCursor: UInt64 = 0
    private var sessionMetadata = MetadataOverlay()
    private var stateRequests: UInt64 = 0
    private var selectedSessionStateSeq: UInt64 = 0
    private var sessionStreamUnsupported = false
    private var unsupportedRefreshTask: Task<Void, Never>?
    private var sessionReload: (id: String, task: Task<Void, Never>)?
    private var pendingPairing: PairingLink?
    private var pushDeviceToken: String?
    private var pushStatus: [String: String] = [:]
    private var registeringPush: Set<String> = []
    private let networkSession: URLSession
    private let automaticallyConnect: Bool
    private let previewPaired: Bool

    var isPaired: Bool { !hostOrder.isEmpty && (previewPaired || connections.values.contains { !$0.isPreview }) }
    var currentSessions: [SessionState] { snapshot?.sessions.filter(\.current) ?? [] }
    var deliveryProblems: [DeliveryProblem] { snapshot?.deliveryProblems ?? [] }
    var showsHostNames: Bool { Set((snapshot?.sessions ?? []).compactMap(\.host?.id)).count > 1 || hostOrder.count > 1 }

    init(autoConnect: Bool = true, session: URLSession = .shared) {
        networkSession = session
        automaticallyConnect = autoConnect
        previewPaired = false
        voiceHostID = UserDefaults.standard.string(forKey: Self.voiceHostKey)
        for credential in PairedCredential.all() { add(HostConnection(credential: credential, session: session)) }
        rebuild()
        Task { await retireLegacyPairing() }
        if autoConnect { connect() }
    }

    init(api: AgenthailAPI, paired: Bool = false, hostID: String = "computer", hostName: String = "computer") {
        networkSession = .shared
        automaticallyConnect = false
        previewPaired = paired
        add(HostConnection(id: hostID, name: hostName, api: api))
        rebuild()
    }

    deinit {
        sessionStreamTask?.cancel()
        sessionReload?.task.cancel()
        unsupportedRefreshTask?.cancel()
        sessionMetadataTask?.cancel()
    }

    private func add(_ connection: HostConnection) {
        connection.delegate = self
        connections[connection.id] = connection
        if !hostOrder.contains(connection.id) { hostOrder.append(connection.id) }
    }

    func setHostSnapshot(_ value: DashboardSnapshot?, host hostID: String? = nil) {
        guard let connection = hostID.flatMap({ connections[$0] }) ?? hostOrder.first.flatMap({ connections[$0] }) else { return }
        connection.setSnapshot(value)
    }

    func connection(_ hostID: String) -> HostConnection? { connections[hostID] }

    private func rebuild() {
        let ordered = hostOrder.compactMap { connections[$0] }
        let views = ordered.map(\.view)
        catalog = HostCatalog(hosts: views)
        let loaded = views.compactMap(\.snapshot)
        if loaded.isEmpty {
            snapshot = nil
        } else {
            let merged = DashboardSnapshot(
                updatedAt: views.map { $0.snapshot?.updatedAt ?? "" }.joined(separator: "|"),
                eventCursor: nil, hostEpoch: nil, catalogSeq: nil,
                daemon: DaemonState(running: true, pid: nil, stale: loaded.contains { $0.daemon.stale == true }, refreshError: nil, host: nil, hosts: catalog.hosts),
                surfaces: [], sessions: catalog.sessions, totalSessions: catalog.sessions.count,
                queue: ordered.flatMap { connection in
                    (connection.snapshot?.queue ?? []).map { item in var item = item; item.hostID = connection.id; return item }
                },
                channels: [], relays: [], history: loaded.flatMap(\.history), attention: catalog.attention,
                deliveryProblems: catalog.deliveryProblems,
                codexRecentHours: loaded.first?.codexRecentHours ?? 24, busyDelivery: nil)
            if snapshot?.hasSamePresentation(as: merged) != true { snapshot = merged }
        }
        hosts = ordered.map { connection in
            PairedHostStatus(id: connection.id, name: connection.name, address: connection.credential.endpoint.host ?? "",
                             reachable: connection.reachable, reconnecting: connection.reconnecting, error: connection.connectionError,
                             surfaces: (connection.snapshot?.surfaces ?? []).filter(\.needsAttention), signin: connection.snapshot?.signin ?? [],
                             peers: connection.snapshot?.daemon.hosts ?? [], notifications: pushStatus[connection.id] ?? "Not enabled")
        }
        creationHosts = catalog.hosts.map { host in
            CreationHost(id: host.id, name: host.name, state: catalog.target(forHost: host.id) == nil ? (host.state == "connected" ? "unreachable" : host.state) : "connected")
        }
        let failing = ordered.filter { $0.connectionError != nil }
        if failing.isEmpty { connectionError = nil }
        else if ordered.count == 1 { connectionError = failing[0].connectionError }
        else { connectionError = failing.map { "\($0.name): \($0.connectionError ?? "")" }.joined(separator: "\n") }
        reconnecting = ordered.contains(where: \.reconnecting)
        if let voice = voiceHostID, connections[voice] == nil { voiceHostID = nil }
        rebindSelectedSession()
    }

    private func server(for sessionID: String) -> HostConnection? {
        catalog.route(for: sessionID).flatMap { connections[$0.hostID] }
    }

    private func rebindSelectedSession() {
        guard let id = selectedSessionID else { return }
        let current = server(for: id)
        guard current !== sessionServer else { return }
        sessionServer = current
        sessionStreamTask?.cancel()
        cancelSessionReload()
        unsupportedRefreshTask?.cancel()
        sessionMetadataTask?.cancel()
        sessionStreamCursor = 0
        sessionStreamUnsupported = false
        sessionRequests = nil
        sessionRequestsKey = nil
        olderActivity = []
        activityCursor = nil
        olderActivityError = nil
        guard current != nil else { return }
        Task { [weak self] in
            await self?.reloadSelectedSession()
            guard let self, self.selectedSessionID == id else { return }
            self.startSessionMetadata(id)
            if let session = self.snapshot?.sessions.first(where: { $0.id == id }) { await self.syncRequests(for: session) }
        }
    }

    private func resolve(_ sessionID: String) throws -> (api: AgenthailAPI, route: SessionRoute) {
        let (connection, route) = try resolveServer(sessionID)
        return (connection.api, route)
    }

    private func resolveServer(_ sessionID: String) throws -> (connection: HostConnection, route: SessionRoute) {
        guard let route = catalog.route(for: sessionID), let connection = connections[route.hostID] else {
            throw AgenthailAPIError.unavailable(unreachableMessage(for: sessionID))
        }
        return (connection, route)
    }

    private func reach(_ hostID: String?) throws -> (api: AgenthailAPI, target: HostTarget) {
        guard let hostID = hostID ?? hostOrder.first(where: { catalog.target(forHost: $0) != nil }) else {
            throw AgenthailAPIError.unavailable("Connect to your computer first.")
        }
        guard let target = catalog.target(forHost: hostID), let connection = connections[target.hostID] else {
            let name = catalog.hosts.first { $0.id == hostID }?.name ?? "That computer"
            throw AgenthailAPIError.unavailable("\(name) can't be reached.")
        }
        return (connection.api, target)
    }

    private func unreachableMessage(for sessionID: String) -> String {
        guard let owner = HostCatalog.owner(of: sessionID) else { return "Connect to your computer first." }
        let name = catalog.hosts.first { $0.id == owner }?.name ?? "The computer running this session"
        return "\(name) can't be reached."
    }

    private func merged(_ row: SessionState, from hostID: String) -> SessionState {
        let names = Dictionary(catalog.hosts.map { ($0.id, $0.name) }, uniquingKeysWith: { first, _ in first })
        return HostCatalog.merge(row, servedBy: hostID, ownerName: { names[$0] }, reachable: connections[hostID]?.reachable ?? false).row
    }

    func routedSession(_ session: SessionState) -> SessionState? {
        guard let route = catalog.route(for: session.id) else { return nil }
        var routed = session
        routed.id = route.sessionID
        return routed
    }

    func routedAPI(for sessionID: String) -> AgenthailAPI? { (try? resolve(sessionID))?.api }

    func busyDelivery(for sessionID: String) -> String? {
        catalog.route(for: sessionID).flatMap { connections[$0.hostID]?.snapshot?.busyDelivery }
    }

    var voiceHost: PairedHostStatus? {
        hosts.first { $0.id == voiceHostID } ?? hosts.first
    }

    func canCallByVoice(_ session: SessionState) -> Bool {
        guard let voice = voiceHost, voice.reachable, session.host?.id == voice.id,
              let route = catalog.route(for: session.id) else { return false }
        return route.hostID == voice.id
    }

    func connectionState(for sessionID: String) -> (error: String?, reconnecting: Bool) {
        let hostID = catalog.route(for: sessionID)?.hostID ?? catalog.server(of: sessionID) ?? HostCatalog.owner(of: sessionID)
        guard let connection = hostID.flatMap({ connections[$0] }) else { return (nil, false) }
        return (connection.connectionError, connection.reconnecting)
    }

    func voiceTargetID(for session: SessionState) -> String {
        catalog.route(for: session.id)?.sessionID ?? ""
    }

    func voiceSessionID(_ id: String) -> String {
        guard let voice = voiceHost else { return id }
        return HostCatalog.qualify(voice.id, id)
    }

    func dismissDeliveryProblem(_ problem: DeliveryProblem) async throws {
        guard let hostID = problem.hostID, let connection = connections[hostID], connection.reachable else {
            throw AgenthailAPIError.unavailable("Connect to your computer first.")
        }
        let key = "delivery:\(problem.id)"
        guard !pendingControls.contains(key) else { return }
        pendingControls.insert(key)
        defer { pendingControls.remove(key) }
        try await connection.api.action("delivery-dismiss", deliveryID: problem.deliveryId)
        connection.updateSnapshot { $0.deliveryProblems = $0.deliveryProblems?.filter { $0.deliveryId != problem.deliveryId } }
    }

    private func deliveryLabel(_ evidence: String?, detail: String?) -> String {
        switch evidence {
        case "submitted": return detail ?? "Submitted"
        case "queued": return detail ?? "Queued"
        case "transport_accepted": return detail ?? "Accepted; not yet confirmed"
        case "delivered": return detail ?? "Sent"
        case "failed": return "Delivery failed"
        case "unknown", nil: return "Submitted"
        case "held": return "Delivery held. Review it in Inbox before retrying."
        case "canceled": return "Instruction canceled."
        default: return "Delivery status: \(evidence ?? "unknown")"
        }
    }

    func turnSettings(for sessionID: String) -> TurnSettings {
        turnSettingsDrafts[sessionID] ?? TurnSettings()
    }

    func setTurnSettings(_ settings: TurnSettings, for sessionID: String) {
        if settings.isEmpty { turnSettingsDrafts.removeValue(forKey: sessionID) }
        else { turnSettingsDrafts[sessionID] = settings }
    }

    func clearTurnSettings(for sessionID: String) {
        turnSettingsDrafts.removeValue(forKey: sessionID)
    }

    func loadSubagents(of session: SessionState) async {
        guard (session.hiddenSubagents?.count ?? 0) > 0 else {
            fetchedSubagents.drop(session.id)
            return
        }
        let requestServer = server(for: session.id)
        do {
            let (connection, route) = try resolveServer(session.id)
            let loaded = try await connection.api.subagents(of: route.sessionID)
            guard !Task.isCancelled, server(for: session.id) === connection else { return }
            fetchedSubagents.store(loaded.map { merged($0, from: route.hostID) }, for: session.id)
        } catch {
            if !Task.isCancelled, server(for: session.id) === requestServer { fetchedSubagents.fail(AgenthailAPIError.userMessage(error), for: session.id) }
        }
    }

    func subagents(of sessionID: String) -> [SessionState] {
        SessionFamilies.children(of: sessionID, listed: snapshot?.sessions ?? [], fetched: fetchedSubagents.children(of: sessionID))
    }

    private func hostQueue(_ connection: HostConnection) async throws -> [QueueState] {
        try await connection.api.queuedInstructions().map { item in
            var item = item
            item.hostID = connection.id
            item.sessionId = HostCatalog.qualify(connection.id, item.sessionId)
            item.sourceSessionId = item.sourceSessionId.map { HostCatalog.qualify(connection.id, $0) }
            return item
        }
    }

    func queuedInstructions() async throws -> [QueueState] {
        let reachable = hostOrder.compactMap { connections[$0] }.filter(\.reachable)
        guard !reachable.isEmpty else { throw AgenthailAPIError.unavailable("Connect to your computer first.") }
        var items: [QueueState] = []
        var failure: Error?
        for connection in reachable {
            do { items += try await hostQueue(connection) } catch { failure = error }
        }
        if items.isEmpty, let failure { throw failure }
        return items
    }

    func editSession(id: String, action: String, text: String = "") async throws {
        let (api, route) = try resolve(id)
        guard !pendingControls.contains(id) else { throw AgenthailAPIError.unavailable("An update is already in progress.") }
        if action != "alias" {
            let settable = ["goal-set", "goal-edit", "goal-clear"].contains(action)
            guard let detail = selectedDetail, detail.session.id == id, !detail.readOnly,
                  settable ? detail.capabilities.goalSet : detail.capabilities.goal && ["goal-pause", "goal-resume", "goal-budget"].contains(action) else { throw AgenthailAPIError.unavailable("This session cannot change goals.") }
        }
        pendingControls.insert(id)
        defer { pendingControls.remove(id) }
        if action == "alias" { try await api.nameSession(id: route.sessionID, alias: text) }
        else { try await api.action(action, sessionID: route.sessionID, message: text) }
        await refresh(host: route.hostID)
        await refreshSession(id)
    }

    func updateQueue(_ item: QueueState, retry: Bool) async throws {
        guard let hostID = item.hostID, let connection = connections[hostID], connection.reachable else {
            throw AgenthailAPIError.unavailable("Connect to your computer first.")
        }
        let key = "queue:\(item.listID)"
        guard !pendingControls.contains(key) else { return }
        pendingControls.insert(key)
        defer { pendingControls.remove(key) }
        try await connection.api.action(retry ? "queue-retry" : "queue-cancel", queueID: item.id)
        await connection.refresh(fresh: true)
    }

    func sessionOptions(host: String?) async throws -> SessionCreationOptions {
        let (api, target) = try reach(host)
        return try await api.sessionOptions(host: target.remote)
    }
    func creationRootUsage(root: String, host: String?) async throws -> ClaudeRootUsage {
        let (api, target) = try reach(host)
        return try await api.claudeRootUsage(root: root, host: target.remote)
    }
    func creationModels(surface: String, host: String?) async throws -> [ModelOption] {
        let (api, target) = try reach(host)
        return try await api.creationModels(surface: surface, host: target.remote)
    }
    func prepareNewSessionForm() {
        creationError = nil
        creationWarning = nil
        creationRequestIdentity = nil
        creationRequestKey = nil
    }
    func createSession(surface: String, message: String, cwd: String, model: String, turnSettings: TurnSettings = .init(), claude: ClaudeCreationSettings = .init(), launcher: LauncherOption? = nil, host: String? = nil) async -> Bool {
        guard !creatingSession, !message.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return false }
        let api: AgenthailAPI
        let target: HostTarget
        do { (api, target) = try self.reach(host) } catch {
            creationError = AgenthailAPIError.userMessage(error)
            return false
        }
        creatingSession = true; creationError = nil; creationWarning = nil
        defer { creatingSession = false }
        let identity = CreationRequestIdentity(surface: surface, message: message, cwd: cwd, model: model,
                                               turnSettings: surface == "codex" ? turnSettings : .init(),
                                               claudeFields: claude.fields, launcher: launcher?.id, host: host)
        if creationRequestIdentity != identity || creationRequestKey == nil {
            creationRequestIdentity = identity
            creationRequestKey = UUID().uuidString
        }
        do {
            let receipt = try await api.createSession(surface: surface, message: message, cwd: cwd, model: model, turnSettings: surface == "codex" ? turnSettings : .init(), claude: claude, launcher: launcher, host: target.remote, idempotencyKey: creationRequestKey)
            guard receipt.ok || receipt.unknown == true || receipt.accepted == true else { throw AgenthailAPIError.invalidResponse }
            if receipt.status == "submitted" && receipt.id == nil {
                creationWarning = "Submitted to \(receipt.launcher ?? launcher?.id ?? surface)."
                return false
            }
            creationWarning = receipt.warning
            if let created = receipt.id, !created.isEmpty {
                let id = HostCatalog.qualify(target.remote ?? target.hostID, created)
                let launched = receipt.launcher ?? launcher?.id ?? surface
                deliveryStatus[id] = receipt.status == "submitted" || receipt.unknown == true ? "Submitted to \(launched)." : (surface == "claude" ? "Session started. Waiting for activity." : "Conversation started")
                requestedSessionID = id
            }
            if receipt.id != nil { creationRequestIdentity = nil; creationRequestKey = nil }
            return true
        } catch {
            if case AgenthailAPIError.unavailable(let detail) = error, detail == "Terminal sessions do not support advanced launch settings." {
                creationError = detail
            } else {
                creationError = "Couldn’t complete request. Your session form is still ready."
            }
            return false
        }
    }

    func focusSession(_ session: SessionState) {
        guard session.runtime?.focusable == true, case let (api, route)? = try? resolve(session.id) else { return }
        Task {
            do { try await api.action("session-focus", sessionID: route.sessionID) }
            catch { operationError = error.localizedDescription }
        }
    }

    func sessionAttachment(for item: TimelineItem, sessionID: String) async -> Data? {
        guard let attachment = item.attachment, selectedSessionID == sessionID, case let (connection, route)? = try? resolveServer(sessionID) else { return nil }
        let cacheKey = "\(sessionID):\(attachment.id)"
        if let cached = attachmentCache[cacheKey] { return cached }
        do {
            let data = try await connection.api.sessionAttachment(sessionID: route.sessionID, id: attachment.id)
            guard !Task.isCancelled, selectedSessionID == sessionID, server(for: sessionID) === connection else { return nil }
            if data.count <= attachmentCacheLimit {
                if attachmentCacheBytes + data.count > attachmentCacheLimit {
                    attachmentCache.removeAll()
                    attachmentCacheBytes = 0
                }
                attachmentCache[cacheKey] = data
                attachmentCacheBytes += data.count
            }
            return data
        } catch {
            return nil
        }
    }

    func handlePairingURL(_ url: URL) {
        do {
            pendingPairing = try PairingLink(url: url)
            operationError = nil
            showPairingConfirmation = true
        } catch {
            pendingPairing = nil
            operationError = error.localizedDescription
        }
    }

    private var pendingPairingMatch: HostConnection? {
        pendingPairing.flatMap { connections[$0.hostID] }
    }

    var pairingConfirmationTitle: String {
        if let existing = pendingPairingMatch { return "Reconnect \(existing.name)?" }
        return "Add \(pendingPairing?.hostName ?? pendingPairing?.endpoint.host?.split(separator: ".").first.map(String.init) ?? "this computer")?"
    }

    var pairingConfirmationAction: String { pendingPairingMatch == nil ? "Add computer" : "Reconnect" }

    var pairingConfirmationMessage: String {
        guard let host = pendingPairing?.endpoint.host else { return "" }
        if let existing = pendingPairingMatch { return "This replaces the saved connection to \(existing.name). Your other computers stay paired." }
        return "Agenthail will connect securely to \(host) over Tailscale."
    }

    func confirmPairing() {
        guard let pendingPairing else { return }
        self.pendingPairing = nil
        showPairingConfirmation = false
        pairing = true
        Task {
            var newAPI: AgenthailAPI?
            do {
                let response = try await AgenthailAPI.completePairing(endpoint: pendingPairing.endpoint, secret: pendingPairing.secret, name: UIDevice.current.name, session: networkSession)
                let pairedAPI = AgenthailAPI(baseURL: pendingPairing.endpoint, token: response.token, session: networkSession)
                newAPI = pairedAPI
                let hostID = response.host.id
                guard hostID == pendingPairing.hostID else {
                    throw AgenthailAPIError.unavailable("This pairing code is for a different computer than it names. Show a new code on the computer and try again.")
                }
                let previous = connections[hostID]
                if let previous {
                    if let relayURL = previous.pushRelayURL, let stored = storedPush(hostID) {
                        do {
                            try await PushRelayClient(baseURL: relayURL, session: networkSession).revoke(stored.registration)
                        } catch {
                            try? await pairedAPI.revokeCurrentDevice()
                            throw AgenthailAPIError.unavailable("The previous notification connection could not be revoked. Try pairing again.")
                        }
                    }
                    if !previous.isPreview {
                        try? await previous.api.removePush()
                        try? await previous.api.revokeCurrentDevice()
                    }
                    previous.stop()
                    clearStoredPush(hostID)
                }
                let credential = PairedHostCredential(hostID: hostID, hostName: response.host.name, endpoint: pendingPairing.endpoint, token: response.token, pairedAt: previous == nil ? Date() : PairedCredential.all().first { $0.hostID == hostID }?.pairedAt ?? Date())
                try PairedCredential.save(credential)
                add(HostConnection(credential: credential, session: networkSession))
                attachmentCache.removeAll()
                attachmentCacheBytes = 0
                pairing = false
                operationError = nil
                completedPairings += 1
                rebuild()
                if automaticallyConnect {
                    connections[hostID]?.connect()
                    requestNotifications()
                }
            } catch {
                if let newAPI { try? await newAPI.revokeCurrentDevice() }
                pairing = false
                operationError = error.localizedDescription
            }
        }
    }

    func cancelPairing() {
        pendingPairing = nil
        showPairingConfirmation = false
    }

    func connect() {
        for id in hostOrder { connections[id]?.connect() }
    }

    func reconnect(host hostID: String) {
        connections[hostID]?.connect()
    }

    func resumeConnection() {
        guard isPaired else {
            refreshNotificationRegistration()
            return
        }
        showNotificationAuthorization()
        connect()
    }

    @discardableResult
    func refresh(fresh: Bool = false) async -> Bool {
        var any = false
        for id in hostOrder {
            if let connection = connections[id], await connection.refresh(fresh: fresh) { any = true }
        }
        return any
    }

    private func refresh(host hostID: String) async {
        _ = await connections[hostID]?.refresh(fresh: true)
    }

    func loadSession(_ id: String) async {
        let id = rekeys.resolve(id)
        if let previous = selectedSessionID, previous != id {
            drafts[previous] = composer
            attachmentDrafts[previous] = composerAttachments
        }
        if selectedSessionID != id {
            sessionStreamTask?.cancel()
            cancelSessionReload()
            unsupportedRefreshTask?.cancel()
            sessionMetadataTask?.cancel()
            sessionStreamCursor = 0
            sessionMetadata = MetadataOverlay()
            sessionStreamUnsupported = false
            composer = drafts[id] ?? ""
            composerAttachments = attachmentDrafts[id] ?? []
            attachmentProblem = nil
            selectedDetail = nil
            sessionRequests = nil
            sessionRequestsKey = nil
            olderActivity = []
            activityCursor = nil
            olderActivityError = nil
        }
        selectedSessionID = id
        sessionError = nil
        loadingSession = selectedDetail == nil
        await reloadSelectedSession()
        if selectedSessionID == id { startSessionMetadata(id) }
        if selectedSessionID == id { loadingSession = false }
    }

    func refreshSession(_ id: String) async {
        guard sessionLoadIsCurrent(id, selectedID: selectedSessionID) else { return }
        let requestID = UUID()
        sessionRequestID = requestID
        let seq = nextStateRequest()
        let requestServer = server(for: id)
        do {
            let (connection, route) = try resolveServer(id)
            var detail = try await connection.api.sessionDetail(id: route.sessionID, includeTimeline: true)
            detail.session.id = id
            guard sessionLoadIsCurrent(id, selectedID: selectedSessionID), sessionRequestID == requestID, server(for: id) === connection else { return }
            if !olderActivity.isEmpty {
                let retained = Set(olderActivity.map(\.id) + (detail.timeline?.items.map(\.id) ?? []))
                olderActivity += (selectedDetail?.timeline?.items ?? []).filter { !retained.contains($0.id) }
            }
            var merged = sessionMetadata.apply(to: detail)
            if seq < selectedSessionStateSeq, let current = selectedDetail?.session, current.id == id {
                merged.session.status = current.status
                merged.session.waitingOn = current.waitingOn
                merged.session.backgroundTasks = current.backgroundTasks
            } else {
                selectedSessionStateSeq = seq
            }
            selectedDetail = merged
            if let journalSeq = detail.journalSeq {
                sessionStreamCursor = journalSeq
            }
            if olderActivity.isEmpty { activityCursor = detail.timeline?.nextBefore }
            sessionError = nil
        } catch {
            guard sessionLoadIsCurrent(id, selectedID: selectedSessionID), sessionRequestID == requestID, server(for: id) === requestServer else { return }
            sessionError = error.localizedDescription
        }
        await refreshDeliveries()
    }

    func syncRequests(for session: SessionState) async {
        guard session.capabilities.respond, !session.isReadOnly else {
            sessionRequests = nil
            sessionRequestsKey = nil
            return
        }
        let key = "\(session.id)|\(session.waitingOn?.id ?? "")"
        guard sessionRequestsKey != key else { return }
        sessionRequestsKey = key
        await fetchRequests(session.id, key: key)
    }

    private func fetchRequests(_ id: String, key: String?) async {
        let requestServer = server(for: id)
        do {
            let (connection, route) = try resolveServer(id)
            let requests = try await connection.api.requests(sessionID: route.sessionID)
            guard selectedSessionID == id, sessionRequestsKey == key, server(for: id) === connection else { return }
            sessionRequests = requests
        } catch {
            guard selectedSessionID == id, server(for: id) === requestServer else { return }
            sessionRequestsKey = nil
            operationError = error.localizedDescription
        }
    }

    func respond(to request: SessionRequest, sessionID: String, decision: RequestDecision) async {
        guard answeringRequest == nil else { return }
        answeringRequest = request.id
        defer { answeringRequest = nil }
        do {
            let (api, route) = try resolve(sessionID)
            _ = try await api.respond(sessionID: route.sessionID, requestID: request.id, decision: decision)
            operationError = nil
        } catch AgenthailAPIError.requestStale {
            sessionRequests = sessionRequests?.removing(request.id)
            operationError = "That request was already answered or has expired."
        } catch {
            operationError = error.localizedDescription
        }
        await fetchRequests(sessionID, key: sessionRequestsKey)
        await refreshSession(sessionID)
    }

    func setPermissionMode(_ mode: String, sessionID: String) async {
        do {
            let (api, route) = try resolve(sessionID)
            try await api.setPermissionMode(sessionID: route.sessionID, mode: mode)
            operationError = nil
        } catch {
            operationError = error.localizedDescription
        }
        await fetchRequests(sessionID, key: sessionRequestsKey)
    }

    private func refreshDeliveries() async {
        guard !deliveryQueueIDs.isEmpty, !refreshingDeliveries else { return }
        refreshingDeliveries = true
        defer { refreshingDeliveries = false }
        let expected = deliveryQueueIDs
        var queues: [String: [QueueState]] = [:]
        for (sessionID, queueID) in expected where deliveryQueueIDs[sessionID] == queueID {
            guard let route = catalog.route(for: sessionID), let connection = connections[route.hostID] else { continue }
            if queues[route.hostID] == nil { queues[route.hostID] = (try? await hostQueue(connection)) ?? [] }
            guard let item = queues[route.hostID]?.first(where: { $0.id == queueID && $0.sessionId == HostCatalog.qualify(route.hostID, route.sessionID) }) else {
                continue
            }
            switch item.isHistorical && item.evidence == "unknown" ? "unknown-history" : item.isHistorical && item.evidence == "failed" ? "failed-history" : item.evidence {
            case "submitted": deliveryStatus[sessionID] = "Submitted to \(item.target)."
            case "queued": deliveryStatus[sessionID] = item.queuedReceipt
            case "unknown": deliveryStatus[sessionID] = "Submitted to \(item.target)."
            case "failed": deliveryStatus[sessionID] = "Delivery failed. Review it in Inbox before retrying."
            case "unknown-history": deliveryStatus[sessionID] = "Delivery was never confirmed. Review it in Inbox history."
            case "failed-history": deliveryStatus[sessionID] = "Delivery failed. Review it in Inbox history."
            case "transport_accepted": deliveryStatus[sessionID] = "Accepted for \(item.target); not yet confirmed."
            case "delivered": deliveryStatus[sessionID] = "Sent to \(item.target)."
            case "held": deliveryStatus[sessionID] = "Delivery held. Review it in Inbox before retrying."
            case "canceled": deliveryStatus[sessionID] = "Instruction canceled."
            default: continue
            }
            if item.isHistorical { deliveryQueueIDs.removeValue(forKey: sessionID) }
        }
    }

    private func startSessionMetadata(_ id: String) {
        sessionMetadataTask?.cancel()
        guard sessionLoadIsCurrent(id, selectedID: selectedSessionID), case let (connection, route)? = try? resolveServer(id) else { return }
        sessionMetadata.metadataRequested()
        sessionMetadataTask = Task { [weak self, connection] in
            do {
                let metadata = try await connection.api.sessionMetadata(id: route.sessionID)
                guard !Task.isCancelled, self?.server(for: id) === connection else { return }
                self?.updateSessionMetadata(for: id) { $0.absorb(metadata) }
            } catch {
                guard !Task.isCancelled, self?.server(for: id) === connection else { return }
                self?.updateSessionMetadata(for: id) { $0.metadataFailed() }
            }
        }
    }

    private func updateSessionMetadata(for id: String, _ change: (inout MetadataOverlay) -> Void) {
        guard sessionLoadIsCurrent(id, selectedID: selectedSessionID), let detail = selectedDetail, detail.session.id == id else { return }
        change(&sessionMetadata)
        selectedDetail = sessionMetadata.apply(to: detail)
    }

    func loadOlderActivity() async {
        guard let id = selectedSessionID, let cursor = activityCursor, cursor > 0, !loadingOlderActivity else { return }
        loadingOlderActivity = true
        defer { loadingOlderActivity = false }
        do {
            let (connection, route) = try resolveServer(id)
            let page = try await connection.api.sessionDetail(id: route.sessionID, includeTimeline: true, timelineBefore: cursor)
            guard selectedSessionID == id, server(for: id) === connection, activityCursor == cursor else { return }
            guard let timeline = page.timeline, timeline.unavailableReason == nil else {
                throw AgenthailAPIError.unavailable(page.timeline?.unavailableReason ?? "Older activity is unavailable.")
            }
            let known = Set(olderActivity.map(\.id) + (selectedDetail?.timeline?.items.map(\.id) ?? []))
            olderActivity = timeline.items.filter { !known.contains($0.id) } + olderActivity
            activityCursor = timeline.nextBefore
            olderActivityError = nil
        } catch {
            if selectedSessionID == id, activityCursor == cursor {
                if case AgenthailAPIError.historyGap = error { activityCursor = nil }
                olderActivityError = error.localizedDescription
            }
        }
    }

    func searchSessions(_ query: String) async {
        searchQuery = query
        searchResults = []
        searchError = nil
        searching = false
        let reachable = hostOrder.compactMap { connections[$0] }.filter(\.reachable)
        guard query.trimmingCharacters(in: .whitespacesAndNewlines).count >= 3, !reachable.isEmpty else { return }
        searching = true
        do {
            try await Task.sleep(for: .milliseconds(350))
            var results: [SessionSearchItem] = []
            var errors: [String] = []
            for connection in reachable {
                do {
                    let response = try await connection.api.searchSessions(query: query)
                    for item in response.results {
                        var item = item
                        let (row, local) = HostCatalog.merge(item.session, servedBy: connection.id, ownerName: { id in self.catalog.hosts.first { $0.id == id }?.name }, reachable: true)
                        item.session = row
                        if let index = results.firstIndex(where: { $0.id == row.id }) {
                            if local { results[index] = item }
                        } else {
                            results.append(item)
                        }
                    }
                    if let remote = response.remoteError, !remote.isEmpty { errors.append(remote) }
                } catch {
                    errors.append("\(connection.name): \(error.localizedDescription)")
                }
            }
            guard !Task.isCancelled, searchQuery == query else { return }
            searchResults = results
            searchError = errors.isEmpty ? nil : errors.joined(separator: "\n")
        } catch {
            guard !Task.isCancelled, searchQuery == query else { return }
            searchError = error.localizedDescription
        }
        if searchQuery == query { searching = false }
    }

    func openSession(_ sessionID: String) {
        requestedSessionID = rekeys.resolve(sessionID)
    }

    private func followRekeys(_ retired: [String: String]) {
        rekeys.record(retired)
        guard !rekeys.isEmpty else { return }
        _ = rekeys.carry(&drafts)
        _ = rekeys.carry(&attachmentDrafts)
        _ = rekeys.carry(&pendingSendRequests)
        var settings = turnSettingsDrafts
        if rekeys.carry(&settings) { turnSettingsDrafts = settings }
        if let requested = requestedSessionID, rekeys.resolve(requested) != requested {
            requestedSessionID = rekeys.resolve(requested)
        } else if let selected = selectedSessionID, rekeys.resolve(selected) != selected {
            requestedSessionID = rekeys.resolve(selected)
        }
    }

    func addAttachments(_ new: [ComposerAttachment], problem: ComposerAttachmentProblem? = nil) {
        let added = ComposerAttachments.adding(new, to: composerAttachments)
        composerAttachments = added.attachments
        attachmentProblem = (problem ?? added.problem)?.message
    }

    func removeAttachment(_ id: UUID) {
        composerAttachments.removeAll { $0.id == id }
        attachmentProblem = nil
    }

    func editAndResend(_ text: String) {
        let edited = text.trimmingCharacters(in: .whitespacesAndNewlines)
        composer = composer.isEmpty ? edited : edited + "\n\n" + composer
    }

    func pickHolder(_ holder: SessionHolder?, for sessionID: String) {
        HolderPins.shared.pick(holder.map(HolderPin.init), for: sessionID)
        let picks = HolderPins.shared.all
        if holderPicks != picks { holderPicks = picks }
    }

    func send(to session: SessionState) {
        let message = composer.trimmingCharacters(in: .whitespacesAndNewlines)
        let attachments = composerAttachments
        guard !ComposerAttachments.isEmpty(text: message, attachments: attachments),
              let detail = selectedDetail, detail.session.id == session.id,
              !detail.readOnly, !sendingSessionIDs.contains(session.id) else { return }
        let api: AgenthailAPI
        let route: SessionRoute
        do { (api, route) = try self.resolve(session.id) } catch {
            operationError = AgenthailAPIError.userMessage(error)
            return
        }
        let action = "send"
        guard detail.capabilities.send else { return }
        let turnSettings = action == "send" && session.surface == "codex" ? turnSettingsDrafts[session.id] ?? TurnSettings() : TurnSettings()
        let idempotencyKey: String
        if let pending = pendingSendRequests[session.id], pending.message == message, pending.attachments == attachments, pending.turnSettings == turnSettings {
            idempotencyKey = pending.idempotencyKey
        } else {
            idempotencyKey = UUID().uuidString
            pendingSendRequests[session.id] = PendingSendRequest(message: message, attachments: attachments, turnSettings: turnSettings, idempotencyKey: idempotencyKey)
        }
        sendingSessionIDs.insert(session.id)
        deliveryQueueIDs.removeValue(forKey: session.id)
        deliveryStatus[session.id] = "Sending…"
        composer = ""
        drafts[session.id] = ""
        composerAttachments = []
        attachmentDrafts[session.id] = []
        attachmentProblem = nil
        Task {
            defer { sendingSessionIDs.remove(session.id) }
            do {
                let uploaded = try await api.upload(attachments, sessionID: route.sessionID).map(\.id)
                let response = try await api.sendInstruction(action: action, sessionID: route.sessionID, message: message, turnSettings: turnSettings, author: "person", attachments: uploaded, idempotencyKey: idempotencyKey)
                if let deliveryID = response.result?.deliveryId { deliveryIDs[session.id] = deliveryID }
                deliveryStatus[session.id] = deliveryLabel(response.result?.evidence, detail: response.result?.detail)
                if response.result?.evidence == "queued", let queueID = response.result?.queueId {
                    deliveryQueueIDs[session.id] = queueID
                }
                await refreshSession(session.id)
                if action == "send", turnSettingsDrafts[session.id] == turnSettings {
                    clearTurnSettings(for: session.id)
                }
                pendingSendRequests.removeValue(forKey: session.id)
            } catch {
                deliveryIDs.removeValue(forKey: session.id)
                deliveryStatus[session.id] = "Couldn’t complete request. Draft kept."
                operationError = AgenthailAPIError.userMessage(error)
                if selectedSessionID == session.id {
                    composer = composer.isEmpty ? message : message + "\n\n" + composer
                    composerAttachments = ComposerAttachments.adding(composerAttachments, to: attachments).attachments
                } else {
                    let newer = drafts[session.id] ?? ""
                    drafts[session.id] = newer.isEmpty ? message : message + "\n\n" + newer
                    attachmentDrafts[session.id] = ComposerAttachments.adding(attachmentDrafts[session.id] ?? [], to: attachments).attachments
                }
            }
        }
    }

    func action(_ action: String, session: SessionState, model: String? = nil, message: String? = nil) {
        guard !pendingControls.contains(session.id) else { return }
        let api: AgenthailAPI
        let route: SessionRoute
        do { (api, route) = try self.resolve(session.id) } catch {
            operationError = AgenthailAPIError.userMessage(error)
            return
        }
        pendingControls.insert(session.id)
        Task {
            defer { pendingControls.remove(session.id) }
            do {
                if let receipt = try await api.control(action, sessionID: route.sessionID, message: message, model: model), receipt.requestId != nil || receipt.output != nil {
                    controlOutput[session.id] = [receipt.output, receipt.detail, receipt.requestId.map { "Request \($0)" }].compactMap { $0 }.joined(separator: "\n")
                }
                await refresh(host: route.hostID)
                await refreshSession(session.id)
            } catch {
                operationError = error.localizedDescription
            }
        }
    }

    func setArchived(sessionID: String, _ archived: Bool) async {
        do {
            let (api, route) = try resolve(sessionID)
            try await api.action(archived ? "session-archive" : "session-unarchive", sessionID: route.sessionID)
            operationError = nil
            await refresh(host: route.hostID)
        } catch {
            operationError = error.localizedDescription
        }
    }

    func setProjectArchived(_ projectID: String, _ archived: Bool) async {
        do {
            guard let owner = HostCatalog.owner(of: projectID) else { throw AgenthailAPIError.unavailable("Connect to your computer first.") }
            let (api, target) = try reach(owner)
            let id = target.remote == nil ? String(projectID.dropFirst(owner.count + HostCatalog.separator.count)) : projectID
            _ = try await api.projectAction(archived ? "project-archive" : "project-unarchive", projectID: id)
            operationError = nil
            await refresh(host: target.hostID)
        } catch {
            operationError = error.localizedDescription
        }
    }

    func removeHost(_ hostID: String) {
        guard let connection = connections[hostID] else { return }
        let relay = connection.pushRelayURL.map { PushRelayClient(baseURL: $0, session: networkSession) }
        let stored = storedPush(hostID)
        Task {
            do {
                try? await connection.api.removePush()
                try await connection.api.revokeCurrentDevice()
                if let relay, let stored { try? await relay.revoke(stored.registration) }
                clearHost(hostID)
            } catch {
                operationError = nil
                forgetCandidate = hosts.first { $0.id == hostID }
            }
        }
    }

    func forgetHost(_ hostID: String) {
        let relay = connections[hostID]?.pushRelayURL.map { PushRelayClient(baseURL: $0, session: networkSession) }
        let stored = storedPush(hostID)
        clearHost(hostID)
        forgetCandidate = nil
        Task {
            if let relay, let stored { try? await relay.revoke(stored.registration) }
        }
    }

    private func clearHost(_ hostID: String) {
        connections[hostID]?.stop()
        connections.removeValue(forKey: hostID)
        hostOrder.removeAll { $0 == hostID }
        KeychainStore.remove(PairedCredential.account(for: hostID))
        KeychainStore.remove(HostConnection.relayAccount(hostID))
        clearStoredPush(hostID)
        pushStatus.removeValue(forKey: hostID)
        let prefix = hostID + HostCatalog.separator
        func owned(_ id: String) -> Bool { id.hasPrefix(prefix) }
        if let selected = selectedSessionID, owned(selected) {
            sessionStreamTask?.cancel()
            cancelSessionReload()
            unsupportedRefreshTask?.cancel()
            sessionMetadataTask?.cancel()
            selectedDetail = nil
            selectedSessionID = nil
            composer = ""
            composerAttachments = []
        }
        drafts = drafts.filter { !owned($0.key) }
        attachmentDrafts = attachmentDrafts.filter { !owned($0.key) }
        pendingSendRequests = pendingSendRequests.filter { !owned($0.key) }
        deliveryStatus = deliveryStatus.filter { !owned($0.key) }
        deliveryQueueIDs = deliveryQueueIDs.filter { !owned($0.key) }
        searchResults = searchResults.filter { !owned($0.id) }
        attachmentCache.removeAll()
        attachmentCacheBytes = 0
        creationRequestIdentity = nil
        creationRequestKey = nil
        rebuild()
        updateNotificationStatus()
    }

    private func retireLegacyPairing() async {
        let legacy = ["endpoint", "token", "pushRelayURL", "pushRegistration", "pushDeviceToken", "pushEnvironment", "pushEndpoint"]
        guard legacy.contains(where: { KeychainStore.get($0) != nil }) else { return }
        var revoked = true
        if let endpoint = KeychainStore.get("endpoint").flatMap(URL.init(string:)), let token = KeychainStore.get("token") {
            let api = AgenthailAPI(baseURL: endpoint, token: token, session: networkSession)
            try? await api.removePush()
            do { try await api.revokeCurrentDevice() }
            catch AgenthailAPIError.request(let status, _) where status == 401 {}
            catch { revoked = false }
        }
        if let relayURL = KeychainStore.get("pushRelayURL").flatMap(URL.init(string:)),
           let value = KeychainStore.get("pushRegistration"), let data = Data(base64Encoded: value),
           let registration = try? JSONDecoder().decode(PushRegistration.self, from: data) {
            do { try await PushRelayClient(baseURL: relayURL, session: networkSession).revoke(registration) }
            catch AgenthailAPIError.request(let status, _) where status == 401 || status == 404 {}
            catch { revoked = false }
        }
        if revoked { legacy.forEach(KeychainStore.remove) }
    }

    func requestNotifications() {
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound, .badge]) { granted, _ in
            if granted {
                DispatchQueue.main.async { UIApplication.shared.registerForRemoteNotifications() }
            } else {
                Task { @MainActor in await self.disableNotifications(status: "Not allowed") }
            }
        }
    }

    func openNotificationSettings() {
        guard let url = URL(string: UIApplication.openNotificationSettingsURLString) else { return }
        UIApplication.shared.open(url)
    }

    func turnOffNotifications() {
        Task { await disableNotifications(status: "Not enabled") }
    }

    func refreshNotificationRegistration() {
        UNUserNotificationCenter.current().getNotificationSettings { settings in
            let authorization = settings.authorizationStatus.rawValue
            Task { @MainActor in
                switch UNAuthorizationStatus(rawValue: authorization) {
                case .authorized, .provisional, .ephemeral:
                    UIApplication.shared.registerForRemoteNotifications()
                case .denied:
                    await self.disableNotifications(status: "Not allowed")
                default:
                    await self.disableNotifications(status: "Not enabled")
                }
            }
        }
    }

    private func showNotificationAuthorization() {
        UNUserNotificationCenter.current().getNotificationSettings { settings in
            let authorization = settings.authorizationStatus.rawValue
            Task { @MainActor in
                switch UNAuthorizationStatus(rawValue: authorization) {
                case .denied: self.notificationStatus = "Not allowed"
                case .notDetermined: self.notificationStatus = "Not enabled"
                default: break
                }
            }
        }
    }

    func registerPushToken(_ deviceToken: String) {
        pushDeviceToken = deviceToken
        for id in hostOrder {
            if let connection = connections[id], connection.reachable { registerPush(connection, deviceToken: deviceToken) }
        }
        updateNotificationStatus()
    }

    private func registerPush(_ connection: HostConnection, deviceToken: String) {
        let hostID = connection.id
        guard !connection.isPreview, !registeringPush.contains(hostID) else { return }
        guard let relayURL = connection.pushRelayURL else {
            pushStatus[hostID] = "Reconnect to finish setup"
            updateNotificationStatus()
            return
        }
        registeringPush.insert(hostID)
        pushStatus[hostID] = "Enabling"
        updateNotificationStatus()
        Task {
            defer {
                registeringPush.remove(hostID)
                updateNotificationStatus()
            }
            do {
                let relay = PushRelayClient(baseURL: relayURL, session: networkSession)
                let endpoint = connection.credential.endpoint.absoluteString
                if let stored = storedPush(hostID), !stored.registration.needsRenewal, stored.deviceToken == deviceToken,
                   stored.environment == PushRelayClient.environment, stored.endpoint == endpoint {
                    do {
                        try await relay.validate(stored.registration)
                        try await connection.api.configurePush(installationID: stored.registration.installationId, credential: stored.registration.credential)
                        pushStatus[hostID] = "Enabled"
                        return
                    } catch AgenthailAPIError.request(let status, _) where status == 401 {
                        clearStoredPush(hostID)
                    }
                }
                if let previous = storedPush(hostID) { try? await relay.revoke(previous.registration) }
                let registration = try await relay.register(deviceToken: deviceToken, tag: hostID)
                let stored = StoredPush(registration: registration, deviceToken: deviceToken, environment: PushRelayClient.environment, endpoint: endpoint)
                try KeychainStore.set(String(decoding: try JSONEncoder().encode(stored), as: UTF8.self), account: Self.pushAccount(hostID))
                guard connections[hostID] === connection else {
                    try? await relay.revoke(registration)
                    clearStoredPush(hostID)
                    return
                }
                try await connection.api.configurePush(installationID: registration.installationId, credential: registration.credential)
                pushStatus[hostID] = "Enabled"
            } catch {
                pushStatus[hostID] = "Setup failed"
                operationError = "\(connection.name): \(error.localizedDescription)"
            }
        }
    }

    private func updateNotificationStatus() {
        let statuses = hostOrder.filter { !(connections[$0]?.isPreview ?? true) }.map { pushStatus[$0] ?? "Not enabled" }
        if statuses.isEmpty { return }
        if statuses.contains("Setup failed") { notificationStatus = "Setup failed" }
        else if statuses.contains("Enabling") { notificationStatus = "Enabling" }
        else if statuses.allSatisfy({ $0 == "Enabled" }) { notificationStatus = "Enabled" }
        else if statuses.contains("Enabled") || statuses.contains("Reconnect to finish setup") { notificationStatus = "Reconnect to finish setup" }
        else { notificationStatus = statuses.first ?? "Not enabled" }
        rebuild()
    }

    private func startSessionStream(_ id: String) {
        sessionStreamTask?.cancel()
        guard selectedSessionID == id, case let (connection, route)? = try? resolveServer(id) else { return }
        sessionStreamTask = Task {
            let backoff = EventRetryBackoff()
            while !Task.isCancelled, selectedSessionID == id, server(for: id) === connection {
                do {
                    try await connection.api.streamSession(id: route.sessionID, after: sessionStreamCursor, onConnected: {}, onEvent: { [weak self] event in
                        backoff.reset()
                        await self?.applySessionStreamEvent(event, route: route, server: connection)
                    })
                } catch {
                    if Task.isCancelled || selectedSessionID != id || server(for: id) !== connection { return }
                    if case AgenthailAPIError.streamUnsupported = error,
                       snapshot?.sessions.first(where: { $0.id == id })?.capabilities.stream != true {
                        sessionStreamUnsupported = true
                        return
                    }
                    if case AgenthailAPIError.streamGap = error {
                        sessionStreamCursor = 0
                        await refreshSession(id)
                    }
                    let delay = backoff.nextDelay()
                    try? await Task.sleep(for: .seconds(delay))
                }
            }
        }
    }

    private func resumeSessionStream(from snapshot: DashboardSnapshot) {
        guard sessionStreamUnsupported, let id = selectedSessionID,
              snapshot.sessions.first(where: { $0.id == id })?.capabilities.stream == true else { return }
        sessionStreamUnsupported = false
        Task { await reloadSelectedSession() }
    }

    private func reloadSelectedSession() async {
        guard let id = selectedSessionID else { return }
        if let pending = sessionReload, pending.id == id {
            await pending.task.value
            return
        }
        let task = Task { [weak self] in
            await self?.refreshSession(id)
            guard !Task.isCancelled, let self, self.selectedSessionID == id else { return }
            self.startSessionStream(id)
        }
        sessionReload = (id, task)
        await task.value
        if sessionReload?.task == task { sessionReload = nil }
    }

    private func cancelSessionReload() {
        sessionReload?.task.cancel()
        sessionReload = nil
    }

    private func refreshUnsupportedSession(_ id: String) {
        guard sessionStreamUnsupported, selectedSessionID == id else { return }
        unsupportedRefreshTask?.cancel()
        unsupportedRefreshTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(250))
            guard !Task.isCancelled, let self, self.sessionStreamUnsupported, self.selectedSessionID == id else { return }
            await self.refreshSession(id)
        }
    }

    func nextStateRequest() -> UInt64 {
        stateRequests += 1
        return stateRequests
    }

    private func syncSelectedSession(from snapshot: DashboardSnapshot, seq: UInt64) {
        guard seq > selectedSessionStateSeq, var detail = selectedDetail,
              let live = snapshot.sessions.first(where: { $0.id == detail.session.id }) else { return }
        selectedSessionStateSeq = seq
        guard detail.session.status != live.status || detail.session.waitingOn != live.waitingOn || detail.session.backgroundTasks != live.backgroundTasks else { return }
        detail.session.status = live.status
        detail.session.waitingOn = live.waitingOn
        detail.session.backgroundTasks = live.backgroundTasks
        selectedDetail = detail
    }

    func applySessionStreamEvent(_ event: SessionStreamEvent) {
        guard let id = selectedSessionID, let route = catalog.route(for: id), let connection = connections[route.hostID] else { return }
        applySessionStreamEvent(event, route: route, server: connection)
    }

    private func applySessionStreamEvent(_ event: SessionStreamEvent, route: SessionRoute, server connection: HostConnection) {
        guard event.stream == "session", event.sessionId == route.sessionID, let selected = selectedSessionID,
              catalog.route(for: selected) == route, server(for: selected) === connection,
              var detail = selectedDetail, detail.session.id == selected else { return }
        sessionStreamCursor = max(sessionStreamCursor, event.seq)
        if event.data.kind == "context" {
            sessionMetadata.stream(context: event.data.context)
            detail.context = event.data.context
        } else if event.data.kind == "goal" {
            sessionMetadata.stream(goal: event.data.goal)
            detail.goal = event.data.goal
        }
        selectedDetail = detail
        if event.data.op == "reset", event.data.kind == "source-error" {
            sessionError = event.data.reason ?? "The session source restarted."
            return
        }
        guard var timeline = detail.timeline else { return }
        guard !event.data.itemId.isEmpty else { return }
        let item = TimelineItem(id: event.data.itemId, kind: event.data.kind, role: event.data.role, title: event.data.title ?? (event.data.kind == "attachment" ? "Image" : event.data.kind), text: event.data.body ?? (event.data.kind == "attachment" ? "Image attachment" : ""), timestamp: event.data.ts, callId: event.data.callId, status: event.data.status, truncated: event.data.truncated, truncationReason: event.data.truncationReason, bodyRef: event.data.bodyRef, attachment: event.data.attachment, sender: event.data.sender, files: event.data.files, resetsAt: event.data.resetsAt)
        if event.data.op == "remove" {
            timeline.items.removeAll { $0.id == event.data.itemId }
        } else if let index = timeline.items.firstIndex(where: { $0.id == event.data.itemId }) {
            timeline.items[index] = item
        } else {
            timeline.items.append(item)
        }
        detail.timeline = timeline
        selectedDetail = detail
        sessionError = nil
    }

    func retainedSessionBody(for item: TimelineItem, start: Int = 0) async -> RetainedBodyResult? {
        guard let id = selectedSessionID, let ref = item.bodyRef, case let (connection, route)? = try? resolveServer(id) else { return nil }
        let chunkSize = 16 << 10
        let maximum = 8 << 20
        var cursor = max(0, start)
        var total: Int?
        var chunks: [String] = []

        do {
            while cursor < (total ?? maximum) {
                let requestedEnd = min(cursor + chunkSize, maximum)
                let chunk = try await connection.api.sessionStreamBody(id: route.sessionID, ref: ref, start: cursor, end: requestedEnd)
                guard server(for: id) === connection else { return nil }
                if total == nil { total = chunk.total }
                guard chunk.total == total, chunk.start == cursor, chunk.end > cursor, chunk.end <= maximum else {
                    return RetainedBodyResult(text: chunks.joined(), error: "The retained body returned an invalid range.")
                }
                if chunk.end < chunk.total && chunk.body.last == "\u{FFFD}" {
                    return RetainedBodyResult(text: chunks.joined(), error: "The retained body is not valid UTF-8 at a range boundary.")
                }
                chunks.append(chunk.body)
                cursor = chunk.end
                if cursor >= chunk.total { return RetainedBodyResult(text: chunks.joined(), error: nil) }
                if cursor >= maximum {
                    return RetainedBodyResult(text: chunks.joined(), error: "The retained body exceeds the 8 MiB display limit.")
                }
            }
            return RetainedBodyResult(text: chunks.joined(), error: "The retained body could not be completed.")
        } catch {
            guard server(for: id) === connection else { return nil }
            return RetainedBodyResult(text: chunks.joined(), error: error.localizedDescription)
        }
    }

    func hostDidChange(_ host: HostConnection) {
        guard connections[host.id] === host else { return }
        rebuild()
        if let snapshot {
            syncSelectedSession(from: snapshot, seq: host.snapshotSeq)
            resumeSessionStream(from: snapshot)
        }
    }

    func hostRefreshed(_ host: HostConnection) async {
        guard connections[host.id] === host else { return }
        await refreshDeliveries()
    }

    func hostConnected(_ host: HostConnection) {
        guard connections[host.id] === host else { return }
        if let pushDeviceToken { registerPush(host, deviceToken: pushDeviceToken) }
        else { refreshNotificationRegistration() }
    }

    func hostEventStreamConnected(_ host: HostConnection) async {
        guard connections[host.id] === host, let id = selectedSessionID, catalog.route(for: id)?.hostID == host.id else { return }
        await reloadSelectedSession()
    }

    func host(_ host: HostConnection, receivedEventFor entity: String) {
        guard connections[host.id] === host else { return }
        refreshUnsupportedSession(HostCatalog.qualify(host.id, entity))
    }

    func host(_ host: HostConnection, rekeyed retired: [String: String]) {
        guard connections[host.id] === host else { return }
        followRekeys(Dictionary(retired.map { (HostCatalog.qualify(host.id, $0.key), HostCatalog.qualify(host.id, $0.value)) }, uniquingKeysWith: { _, new in new }))
    }

    func host(_ host: HostConnection, removedSession id: String, unlisted: Bool) {
        guard connections[host.id] === host else { return }
        fetchedSubagents.sessionRemoved(HostCatalog.qualify(host.id, id), unlisted: unlisted)
    }

    private static func pushAccount(_ hostID: String) -> String { "push:\(hostID)" }

    private func storedPush(_ hostID: String) -> StoredPush? {
        guard let value = KeychainStore.get(Self.pushAccount(hostID)) else { return nil }
        return try? JSONDecoder().decode(StoredPush.self, from: Data(value.utf8))
    }

    private func disableNotifications(status: String) async {
        var failure: String?
        for id in hostOrder {
            guard let connection = connections[id], !connection.isPreview else { continue }
            do {
                try await connection.api.removePush()
                if let relayURL = connection.pushRelayURL, let stored = storedPush(id) {
                    try? await PushRelayClient(baseURL: relayURL, session: networkSession).revoke(stored.registration)
                }
                clearStoredPush(id)
                pushStatus[id] = status
            } catch {
                pushStatus[id] = status
                failure = "Could not disable notification delivery on \(connection.name). Reconnect and try again. \(error.localizedDescription)"
            }
        }
        notificationStatus = status
        if let failure { operationError = failure }
        rebuild()
    }

    private func clearStoredPush(_ hostID: String) {
        KeychainStore.remove(Self.pushAccount(hostID))
    }
}

final class ComposerDraft: ObservableObject {
    @Published var text = ""
}
