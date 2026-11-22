import Combine
import Foundation
import os
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
    var person: PersonState? = nil
    var build: AgenthailBuild? = nil
    var busyDelivery: String? = nil
}

@MainActor
@Observable
final class AgenthailIOSModel: HostConnectionDelegate, ComposerRouting, ListingRouting {
    static let voiceHostKey = "voiceHost"
    @ObservationIgnored var hostRefreshWait: Duration = HostRefresh.wait
    @ObservationIgnored let agentRoles = AgentRolesStore()

    private(set) var snapshot: DashboardSnapshot? {
        didSet {
            trackFinishedSessions(from: oldValue)
            cells.adopt(snapshot?.sessions ?? [])
            daemon = snapshot?.daemon
            subagentIDs = (snapshot?.sessions ?? []).reduce(into: [:]) { ids, row in
                if let parent = row.subagent?.parentId { ids[parent, default: []].append(row.id) }
            }
            sessionAttention = SessionAttention.of(snapshot?.sessions ?? [])
            sessionListing.rederive()
            if let snapshot {
                HolderPins.shared.prune(to: snapshot.sessions)
                let picks = HolderPins.shared.all
                if holderPicks != picks { holderPicks = picks }
            }
        }
    }
    private(set) var finishedUnseen = FinishedSessions()
    @ObservationIgnored private let onScreen: OnScreenSessions
    private(set) var hosts: [PairedHostStatus] = []
    /// Which transcript rows were opened in each session, kept while the app runs.
    let rowMemory = TranscriptRowMemory()
    private(set) var creationHosts: [CreationHost] = []
    private(set) var projects: [String: ListedProject] = [:]
    private(set) var projectOrder: [String] = []
    private(set) var holderPicks: [String: HolderPin] = [:]
    @ObservationIgnored lazy var decisionFeed = DecisionFeed(key: { [unowned self] in decisionsKey }, load: { [unowned self] in await loadDecisions() })
    let cells = SessionCells()
    private var subagentIDs: [String: [String]] = [:]
    private var daemon: DaemonState?
    private(set) var sessionAttention: [String: SessionAttention] = [:]
    @ObservationIgnored private var detailStorage: SessionDetail?
    private var detailRevision: UInt64 = 0
    @ObservationIgnored let timelineCells = TimelineCells()
    var selectedDetail: SessionDetail? {
        get {
            _ = detailRevision
            return detailStorage
        }
        set {
            detailStorage = newValue
            detailRevision &+= 1
            timelineCells.adopt(newValue?.timeline?.items ?? [])
            paintOpeningSession()
        }
    }

    private func streamText(into detail: SessionDetail) {
        detailStorage = detail
        timelineCells.adopt(detail.timeline?.items ?? [])
        paintOpeningSession()
    }
    var sessionRequests: SessionRequests?
    var answeringRequest: String?
    private(set) var retiredAsks = RetiredAsks()
    @ObservationIgnored private var sessionRequestsKey: String?
    var selectedSessionID: String? {
        didSet {
            guard selectedSessionID != oldValue else { return }
            sessionServer = selectedSessionID.flatMap(server(for:))
            endOpenInterval("left")
        }
    }
    private(set) var connectionError: String?
    private(set) var reconnecting = false
    var operationError: String?
    let projectNotice = TransientNotice()
    var movesSent: Set<String> = []
    var moveNotices = SessionMoveNotices()
    var pairing = false
    private(set) var completedPairings = 0
    let appearance = PhoneAppearance()
    var sessionError: String?
    var loadingSession = false
    private static let signposter = OSSignposter(subsystem: "com.agenthail.ios", category: "SessionOpen")
    @ObservationIgnored private var openInterval: (id: OSSignpostID, state: OSSignpostIntervalState, session: String, painted: Bool, resumes: Bool)?
    private var attachmentCache: [String: Data] = [:]
    @ObservationIgnored private var attachmentCacheBytes = 0
    private let attachmentCacheLimit = 20 * 1024 * 1024
    private let attachmentLoads = SharedLoads<Data?>()
    var deliveryStatus: [String: String] = [:]
    var controlOutput: [String: String] = [:]
    var deliveryIDs: [String: Int64] = [:]
    @ObservationIgnored var deliveryQueueIDs: [String: Int64] = [:]
    @ObservationIgnored private var refreshingDeliveries = false
    var olderActivity: [TimelineItem] = []
    var activityCursor: Int64?
    var loadingOlderActivity = false
    var olderActivityError: String?
    var searchResults: [SessionSearchItem] = []
    var searchError: String?
    var searching = false
    @ObservationIgnored private var searchQuery = ""
    @ObservationIgnored private var sessionRequestID = UUID()
    private(set) var rekeys = SessionRekeys()
    @ObservationIgnored private(set) lazy var composerSession = ComposerSession(router: self)
    @ObservationIgnored private(set) lazy var sessionListing = SessionListing(router: self)
    private struct CreationRequestIdentity: Equatable {
        let surface: String
        let message: String
        let cwd: String
        let model: String
        let launch: LaunchSettings
        let claudeFields: [String: String]
        let launcher: String?
        let host: String?
    }
    @ObservationIgnored private var creationRequestIdentity: CreationRequestIdentity?
    @ObservationIgnored private var creationRequestKey: String?
    var requestedSessionID: String?
    private(set) var routeOpenings = 0
    var forgetCandidate: PairedHostStatus?
    var showPairingConfirmation = false
    var voiceHostID: String? {
        didSet { UserDefaults.standard.set(voiceHostID, forKey: Self.voiceHostKey) }
    }

    var creatingSession = false
    var creationError: String?
    var creationWarning: String?
    private(set) var creationCanStartAgain = false
    var pendingControls: Set<String> = []
    private(set) var fetchedSubagents = SubagentPageCache()
    @ObservationIgnored private var subagentServers: [String: HostConnection?] = [:]
    @ObservationIgnored private var subagentFailedUnreachable: Set<String> = []

    // HostConnection and the catalog are not observed; views read the parts rebuild() derives from them.
    @ObservationIgnored private(set) var connections: [String: HostConnection] = [:]
    private(set) var hostOrder: [String] = []
    private(set) var extensionCatalogs: [String: ExtensionCatalog] = [:]
    private(set) var tileOrder: TileOrderStore?
    @ObservationIgnored private var tileStores: [String: TileOrderStore] = [:]
    @ObservationIgnored private(set) var catalog = HostCatalog()
    private(set) var routing = HostCatalog.Routing()
    private(set) var isPaired = false
    private(set) var showsHostNames = false
    private(set) var usageReaders: [AgenthailAPI] = []
    private(set) var decisionsKey = ""
    private(set) var queue: [QueueState] = []
    private(set) var leadReplacements: [LeadReplacement] = []
    private(set) var waitingSessions: [String] = []
    private(set) var sessionCount = 0
    private(set) var usageSessions: [SessionState] = []
    private(set) var hostGroups: [String: HostGroups] = [:]
    private(set) var sessionProjects: [String: String] = [:]
    private var projectSessionIDs: [String: [String]] = [:]
    @ObservationIgnored private var sessionStreamTask: Task<Void, Never>?
    static let streamPublishInterval: Duration = .milliseconds(100)
    @ObservationIgnored private var sessionMetadataTask: Task<Void, Never>?
    @ObservationIgnored private var sessionServer: HostConnection?
    @ObservationIgnored private(set) var sessionStreamCursor: UInt64 = 0
    @ObservationIgnored private var pageReadsInFlight = 0
    /// Rows streamed while a page read is in flight, so a page read before
    /// some of them arrived can be brought up to date.
    @ObservationIgnored private var rowsDuringPageRead: [SessionStreamEvent] = []
    /// Set when the stream changed what a page would show in a way the shown
    /// detail does not hold, so its position cannot be cached until a page read.
    @ObservationIgnored private var streamOutranPage = false
    @ObservationIgnored private(set) lazy var store: SessionStore = {
        let store = SessionStore(limits: .phone)
        store.pinned = { [unowned self] id in id == selectedSessionID || composerSession.hasDraft(id) }
        store.working = { [unowned self] id in snapshot?.sessions.first { $0.id == id }?.isWorking == true }
        return store
    }()
    private var sessionMetadata = MetadataOverlay()
    @ObservationIgnored private var stateRequests: UInt64 = 0
    @ObservationIgnored private var selectedSessionStateSeq: UInt64 = 0
    @ObservationIgnored private var unlistedStateRead: Task<Void, Never>?
    @ObservationIgnored private var unlistedStatePending: String?
    private var sessionStreamUnsupported = false
    @ObservationIgnored private var unsupportedRefreshTask: Task<Void, Never>?
    @ObservationIgnored private var sessionReload: (id: String, task: Task<Void, Never>)?
    private var pendingPairing: PairingLink?
    @ObservationIgnored private var inactiveSince: Date?
    @ObservationIgnored var tailnetUp: () -> Bool = { Tailnet.isUp() }
    @ObservationIgnored private let notificationDefaults: UserDefaults
    @ObservationIgnored private let notificationAuthorization: () async -> UNAuthorizationStatus
    @ObservationIgnored private(set) lazy var push = PushRegistrar(defaults: notificationDefaults, session: networkSession, connection: { [weak self] in self?.connections[$0] }, order: { [weak self] in self?.hostOrder ?? [] }, changed: { [weak self] in self?.rebuild() }, authorization: notificationAuthorization)
    private let networkSession: URLSession
    private let connectionDrops: ConnectionDrops?
    private let automaticallyConnect: Bool
    private let previewPaired: Bool

    func session(_ id: String?) -> SessionState? { cells.session(id) }
    func setTextScale(_ scale: Double) {
        appearance.setTextScale(scale, api: hostOrder.first.flatMap { connections[$0] }.flatMap { $0.isPreview ? nil : $0.api })
    }
    func pairedHost(owning sessionID: String) -> SessionHost? { daemon?.pairedHost(owning: sessionID) }

    /// Names the lead that replaced a request's session; the computer serving the request gives the id unqualified.
    func leadName(_ id: String, requestFrom sessionID: String) -> String {
        let qualified = HostCatalog.qualify(routing.route(for: sessionID)?.hostID ?? HostCatalog.owner(of: sessionID) ?? "", id)
        return session(qualified)?.displayName ?? id
    }

    init(autoConnect: Bool = true, session: URLSession = .shared, drops: ConnectionDrops? = .shared, composerStore: ComposerStore? = nil, notificationDefaults: UserDefaults = .standard, notificationAuthorization: @escaping () async -> UNAuthorizationStatus = PushRegistrar.systemAuthorization, onScreen: OnScreenSessions = OnScreenSessions()) {
        self.onScreen = onScreen
        self.notificationDefaults = notificationDefaults
        self.notificationAuthorization = notificationAuthorization
        networkSession = session
        connectionDrops = drops
        automaticallyConnect = autoConnect
        previewPaired = false
        voiceHostID = UserDefaults.standard.string(forKey: Self.voiceHostKey)
        for credential in PairedCredential.all() { add(HostConnection(credential: credential, session: session, drops: drops)) }
        rebuild()
        composerSession.start(store: composerStore)
#if DEBUG
        for hostID in LiveTestHosts.loopbackHostIDs() { clearHost(hostID) }
#endif
        Task { await retireLegacyPairing() }
        if autoConnect { connect() }
    }

    init(api: AgenthailAPI, paired: Bool = false, hostID: String = "computer", hostName: String = "computer", composerStore: ComposerStore? = nil) {
        onScreen = OnScreenSessions()
        notificationDefaults = .standard
        notificationAuthorization = PushRegistrar.systemAuthorization
        networkSession = .shared
        connectionDrops = nil
        automaticallyConnect = false
        previewPaired = paired
        add(HostConnection(id: hostID, name: hostName, api: api))
        rebuild()
        composerSession.start(store: composerStore)
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
        if !connection.isPreview {
            let catalog = extensionCatalogs[connection.id] ?? ExtensionCatalog()
            catalog.attach(connection.api, hostID: connection.id, hostName: connection.name)
            extensionCatalogs[connection.id] = catalog
        }
    }

    var extensionHosts: [ExtensionCatalog] { hostOrder.compactMap { extensionCatalogs[$0] } }

    var extensionEntries: [ExtensionEntry] { extensionHosts.flatMap(\.entries) }

    var hostsWithoutExtensions: [String] {
        let direct = Set(hostOrder)
        return hosts.flatMap(\.peers).filter { !direct.contains($0.id) }.map(\.name).reduce(into: [String]()) { names, name in
            if !names.contains(name) { names.append(name) }
        }
    }

    func extensionEntry(_ id: String) -> ExtensionEntry? {
        extensionEntries.first { $0.id == id }
    }

    func hostExtensionsChanged(_ host: HostConnection) {
        guard connections[host.id] === host, host.reachable, let catalog = extensionCatalogs[host.id], catalog.list != nil else { return }
        catalog.attach(host.api, hostID: host.id, hostName: host.name)
        catalog.refresh()
    }

    func sessionAppeared(_ id: String) {
        onScreen.appeared(id)
        if finishedUnseen.contains(id) { finishedUnseen.markSeen(id) }
    }

    func sessionDisappeared(_ id: String) {
        onScreen.disappeared(id)
    }

    private func trackFinishedSessions(from previous: DashboardSnapshot?) {
        guard let previous, let current = snapshot else { return }
        var next = finishedUnseen
        next.track(previous: previous.sessions, current: current.sessions) { onScreen.contains($0) }
        if next != finishedUnseen { finishedUnseen = next }
    }

    func setHostSnapshot(_ value: DashboardSnapshot?, view: ScopePage.View? = nil, host hostID: String? = nil) {
        guard let connection = hostID.flatMap({ connections[$0] }) ?? hostOrder.first.flatMap({ connections[$0] }) else { return }
        connection.setSnapshot(value, view: view)
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
                channels: [], relays: [], history: [],
                codexRecentHours: loaded.first?.codexRecentHours ?? 24, busyDelivery: nil,
                leadReplacements: catalog.leadReplacements)
            if snapshot?.hasSamePresentation(as: merged) != true { snapshot = merged }
        }
        sessionListing.hostsChanged()
        store.carry(rekeys)
        hosts = ordered.map { connection in
            PairedHostStatus(id: connection.id, name: connection.name, address: connection.credential.endpoint.host ?? "",
                             reachable: connection.reachable, reconnecting: connection.reconnecting, error: connection.connectionError,
                             surfaces: (connection.snapshot?.surfaces ?? []).filter(\.needsAttention), signin: connection.snapshot?.signin ?? [],
                             peers: connection.snapshot?.daemon.hosts ?? [], notifications: push.status(for: connection.id),
                             person: connection.snapshot?.person, build: connection.build, busyDelivery: connection.snapshot?.busyDelivery)
        }
        let names = Dictionary(ordered.map { ($0.id, $0.name) } + catalog.hosts.map { ($0.id, $0.name) }, uniquingKeysWith: { first, _ in first })
        let routes = catalog
        let (listed, listedOrder) = ProjectListing.merge(ordered.map { ProjectListing.Server(id: $0.id, projects: $0.snapshot?.projects ?? []) },
                                                         name: { names[$0] }, reachable: { routes.target(forHost: $0) != nil })
        if projects != listed { projects = listed }
        if projectOrder != listedOrder { projectOrder = listedOrder }
        creationHosts = catalog.hosts.map { host in
            CreationHost(id: host.id, name: host.name, state: catalog.target(forHost: host.id) == nil ? (host.state == "connected" ? "unreachable" : host.state) : "connected")
        }
        connectionError = HostProblem.summary(ordered.compactMap { connection in
            connection.connectionError.map { HostProblem.Failing(name: connection.name, error: $0, problem: connection.problem, endpoint: connection.credential.endpoint) }
        }, hostCount: ordered.count, tailnetUp: tailnetUp)
        reconnecting = ordered.contains(where: \.reconnecting)
        deriveParts(ordered)
        if let voice = voiceHostID, connections[voice] == nil { voiceHostID = nil }
        rebindSelectedSession()
        refetchMovedSubagents()
    }

    private static func previewTileDefaults() -> UserDefaults {
        let suite = "agenthail.preview.tiles"
        UserDefaults.standard.removePersistentDomain(forName: suite)
        return UserDefaults(suiteName: suite) ?? .standard
    }

    private func syncTileOrder(_ first: HostConnection?) {
        guard let first else { return }
        let host = first.id
        let store = tileStores[host] ?? TileOrderStore(defaults: first.isPreview ? Self.previewTileDefaults() : .standard, key: "projectTiles." + host)
        tileStores[host] = store
        if tileOrder !== store { tileOrder = store }
        let prefix = host + HostCatalog.separator
        let ids = TileOrderStore.IDs(inward: { HostCatalog.qualify(host, $0) }, outward: { $0.hasPrefix(prefix) ? String($0.dropFirst(prefix.count)) : $0 })
        let api = first.api
        store.adopt(first.reachable ? first.snapshot?.tileOrder : nil, ids: ids) { try await api.putTileOrder($0) }
    }

    private func deriveParts(_ ordered: [HostConnection]) {
        let sessions = snapshot?.sessions ?? []
        routing = catalog.routing
        isPaired = !hostOrder.isEmpty && (previewPaired || connections.values.contains { !$0.isPreview })
        showsHostNames = Set(sessions.compactMap(\.host?.id)).count > 1 || hostOrder.count > 1
        let readers = ordered.filter(\.reachable).map(\.api)
        if !usageReaders.elementsEqual(readers, by: ===) { usageReaders = readers }
        sessionListing.read()
        syncTileOrder(ordered.first)
        let reached = catalog.hosts.map { host in "\(host.id):\(host.state):\(catalog.target(forHost: host.id).map { "\($0.hostID)>\($0.remote ?? "")" } ?? "none")" }
        decisionsKey = (ordered.map { "\($0.id):\($0.shownReachable):\($0.snapshot?.decisionsRev ?? "older")" } + reached).joined(separator: ",")
        queue = snapshot?.queue ?? []
        leadReplacements = snapshot?.leadReplacements ?? []
        waitingSessions = sessions.filter { $0.waitingOn != nil || $0.pendingAskID != nil }.map { "\($0.id):\($0.waitingOn?.id ?? $0.pendingAskID ?? "")" }
        sessionCount = sessions.count
        usageSessions = sessions.filter { $0.archived != true && ($0.isWorking || $0.current) }
        hostGroups = Dictionary(ordered.compactMap { host in host.snapshot.map { (host.id, HostGroups(channels: $0.channels, relays: $0.relays)) } }, uniquingKeysWith: { first, _ in first })
        sessionProjects = Dictionary(sessions.compactMap { row in row.project.map { (row.id, $0.id) } }, uniquingKeysWith: { first, _ in first })
        projectSessionIDs = sessionListing.inDaemonOrder(sessions).reduce(into: [:]) { ids, row in
            if let project = row.project?.id { ids[project, default: []].append(row.id) }
        }
    }

    private func refetchMovedSubagents() {
        for (parent, recorded) in subagentServers {
            let current = server(for: parent)
            let recovered = subagentFailedUnreachable.contains(parent) && current?.reachable == true
            guard current !== recorded || recovered else { continue }
            subagentServers[parent] = nil
            subagentFailedUnreachable.remove(parent)
            if fetchedSubagents.holds(parent) { fetchedSubagents.refetch(parent) }
        }
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
        store.rereadCommands(id)
        sessionStreamCursor = 0
        sessionStreamUnsupported = false
        if sessionRequests != nil { sessionRequests = nil }
        sessionRequestsKey = nil
        olderActivity = []
        activityCursor = nil
        olderActivityError = nil
        attachmentCache.removeAll()
        attachmentCacheBytes = 0
        guard current != nil else { return }
        Task { [weak self] in
            await self?.reloadSelectedSession()
            guard let self, self.selectedSessionID == id else { return }
            self.loadCommands(id)
            self.startSessionMetadata(id)
            if let session = self.snapshot?.sessions.first(where: { $0.id == id }) { await self.syncRequests(for: session) }
        }
    }

    func resolve(_ sessionID: String) throws -> (api: AgenthailAPI, route: SessionRoute) {
        let (connection, route) = try resolveServer(sessionID)
        return (connection.api, route)
    }

    func openRequests(of sessionID: String) async -> SessionRequests? {
        guard let (api, route) = try? resolve(sessionID) else { return nil }
        return try? await api.requests(sessionID: route.sessionID)
    }

    func api(for sessionID: String) -> AgenthailAPI? { try? resolve(sessionID).api }

    func runningServer(_ sessionID: String) -> (connection: HostConnection, route: SessionRoute)? {
        guard let (connection, route) = try? resolveServer(sessionID), HostCatalog.owner(of: sessionID) == route.hostID else { return nil }
        return (connection, route)
    }

    func resolveServer(_ sessionID: String) throws -> (connection: HostConnection, route: SessionRoute) {
        guard let route = catalog.route(for: sessionID), let connection = connections[route.hostID] else {
            throw AgenthailAPIError.unavailable(unreachableMessage(for: sessionID))
        }
        return (connection, route)
    }

    func reach(_ hostID: String?) throws -> (api: AgenthailAPI, target: HostTarget) {
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
        guard let route = routing.route(for: session.id) else { return nil }
        var routed = session
        routed.id = route.sessionID
        return routed
    }

    func routedAPI(for sessionID: String) -> AgenthailAPI? { (try? resolve(sessionID))?.api }

    func busyDelivery(for sessionID: String) -> String? {
        routing.route(for: sessionID).flatMap { route in hosts.first { $0.id == route.hostID }?.busyDelivery }
    }

    var voiceHost: PairedHostStatus? {
        hosts.first { $0.id == voiceHostID } ?? hosts.first
    }

    func canCallByVoice(_ session: SessionState) -> Bool {
        guard let voice = voiceHost, voice.reachable, session.host?.id == voice.id,
              let route = routing.route(for: session.id) else { return false }
        return route.hostID == voice.id
    }

    func connectionState(for sessionID: String) -> (error: String?, reconnecting: Bool) {
        let hostID = routing.route(for: sessionID)?.hostID ?? routing.server(of: sessionID) ?? HostCatalog.owner(of: sessionID)
        guard let host = hosts.first(where: { $0.id == hostID }) else { return (nil, false) }
        return (host.error, host.reconnecting)
    }

    func voiceTargetID(for session: SessionState) -> String {
        routing.route(for: session.id)?.sessionID ?? ""
    }

    func voiceSessionID(_ id: String) -> String {
        guard let voice = voiceHost else { return id }
        return HostCatalog.qualify(voice.id, id)
    }

    func acknowledgeLeadReplacement(_ replacement: LeadReplacement) async throws {
        guard let hostID = replacement.hostID, let connection = connections[hostID], connection.shownReachable else {
            throw AgenthailAPIError.unavailable("Connect to your computer first.")
        }
        let key = "lead-replacement:\(replacement.listID)"
        guard !pendingControls.contains(key) else { return }
        pendingControls.insert(key)
        defer { pendingControls.remove(key) }
        try await connection.api.acknowledgeLeadReplacement(replacement.id)
        connection.updateSnapshot { $0.leadReplacements = $0.leadReplacements?.filter { $0.id != replacement.id } }
    }

    func deliveryLabel(_ receipt: DeliveryReceipt?) -> String {
        PersonMessageNote.receiptLine(deliveryLabel(receipt?.evidence, detail: receipt?.detail), receipt: receipt)
    }

    private func deliveryLabel(_ evidence: String?, detail: String?) -> String {
        switch evidence {
        case "submitted": return detail ?? "Submitted"
        case "queued": return detail ?? "Queued"
        case "transport_accepted", "delivered": return detail ?? "Sent"
        case "failed": return "Delivery failed"
        case "unknown", nil: return "Submitted"
        case "held": return "Held by the receiver."
        case "canceled": return "Instruction canceled."
        default: return "Delivery status: \(evidence ?? "unknown")"
        }
    }

    func loadSubagents(of session: SessionState) async {
        guard (session.hiddenSubagents?.count ?? 0) > 0 else {
            fetchedSubagents.drop(session.id)
            subagentServers[session.id] = nil
            subagentFailedUnreachable.remove(session.id)
            return
        }
        let requestServer = server(for: session.id)
        do {
            let (connection, route) = try resolveServer(session.id)
            let loaded = try await connection.api.subagents(of: route.sessionID)
            guard !Task.isCancelled, server(for: session.id) === connection else { return }
            fetchedSubagents.store(loaded.map { merged($0, from: route.hostID) }, for: session.id)
            subagentServers.updateValue(connection, forKey: session.id)
            subagentFailedUnreachable.remove(session.id)
        } catch {
            guard !Task.isCancelled, server(for: session.id) === requestServer else { return }
            fetchedSubagents.fail(AgenthailAPIError.userMessage(error), for: session.id)
            subagentServers.updateValue(requestServer, forKey: session.id)
            if requestServer?.reachable != true { subagentFailedUnreachable.insert(session.id) }
        }
    }

    func subagents(of sessionID: String) -> [SessionState] {
        SessionFamilies.children(of: sessionID, listed: (subagentIDs[sessionID] ?? []).compactMap(session), fetched: fetchedSubagents.children(of: sessionID))
    }

    func answerDecision(_ request: SessionRequest, sessionID: String, decision: RequestDecision) async -> DecisionAnswer {
        guard answeringRequest == nil else { return .busy }
        answeringRequest = request.id
        defer { answeringRequest = nil }
        var failure: RequestAnswerFailure?
        do {
            let (api, route) = try resolve(sessionID)
            _ = try await api.respond(sessionID: route.sessionID, requestID: request.id, decision: decision)
            composerSession.refusedSendDeleted(ask: request.id, choice: decision.choices.first)
        } catch {
            failure = RequestAnswerFailure(error)
        }
        if RequestAnswerFailure.retires(request, failure: failure) {
            retiredAsks.retire(request.id, in: sessionID, receipt: failure == nil ? AnswerReceipt(request, decision) : nil)
        }
        await refreshSession(sessionID)
        return failure.map(DecisionAnswer.failed) ?? .answered
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

    @discardableResult
    func cancelQueue(_ item: QueueState) async throws -> Bool {
        guard let hostID = item.hostID else { throw AgenthailAPIError.unavailable("Connect to your computer first.") }
        let (api, target) = try reach(hostID)
        let key = "queue:\(hostID)/\(item.id)"
        guard !pendingControls.contains(key) else { return false }
        pendingControls.insert(key)
        defer { pendingControls.remove(key) }
        try await api.action("queue-cancel", queueID: item.id, host: target.remote)
        await connections[target.hostID]?.refresh(fresh: true)
        return true
    }

    /// Cancels a queued message and puts it back ahead of its session's draft.
    @discardableResult
    func takeBack(_ item: QueueState) async throws -> Bool {
        guard let hostID = item.hostID else { throw AgenthailAPIError.unavailable("Connect to your computer first.") }
        let (api, target) = try reach(hostID)
        let key = "queue:\(item.listID)"
        guard !pendingControls.contains(key) else { return false }
        pendingControls.insert(key)
        defer { pendingControls.remove(key) }
        let cancelled = try await api.cancelQueued(item, host: target.remote)
        await connections[target.hostID]?.refresh(fresh: true)
        composerSession.returnToDraft(cancelled.message ?? item.message, attachments: cancelled.attachments ?? [], id: draftKey(for: item))
        return true
    }

    func sessionQueue(_ sessionID: String) async throws -> [QueueState] {
        let (api, route) = try resolve(sessionID)
        let owner = HostCatalog.owner(of: sessionID) ?? route.hostID
        let items = try await api.activeQueue(sessionID: route.sessionID, timeout: InboxPage.hostTimeout)
        return items.map { InboxPage.qualified($0, host: owner) }
    }

    func queueSteer(_ item: QueueState) -> (capabilities: Capabilities, refusal: String?)? {
        guard !item.isHistorical, !QueueSteer.waiting([item]).isEmpty else { return nil }
        let id = item.sessionId
        let known = session(id)
            ?? selectedDetail.flatMap { $0.session.id == id ? $0.sessionState(open: false, host: pairedHost(owning: id)) : nil }
        guard let session = known, session.status == "busy", session.waitingOn == nil, session.capabilities.steer, !session.isReadOnly else { return nil }
        return (session.capabilities, QueueSteer.refusal(item, capabilities: session.capabilities))
    }

    @discardableResult
    func steerQueued(_ item: QueueState, capabilities: Capabilities) async throws -> Bool {
        guard QueueSteer.refusal(item, capabilities: capabilities) == nil else { return false }
        guard let hostID = item.hostID else { throw AgenthailAPIError.unavailable("Connect to your computer first.") }
        let (ownerAPI, target) = try reach(hostID)
        let id = draftKey(for: item)
        let (api, route) = try resolve(id)
        let key = "queue:\(item.listID)"
        guard !pendingControls.contains(key) else { return false }
        pendingControls.insert(key)
        defer { pendingControls.remove(key) }
        let cancelled = try await ownerAPI.cancelQueued(item, host: target.remote)
        let message = cancelled.message ?? item.message
        let attachments = cancelled.attachments ?? []
        do {
            let receipt = try await api.steerQueued(message: message, attachments: attachments, sessionID: route.sessionID)
            deliveryStatus[id] = deliveryLabel(receipt.result)
        } catch {
            composerSession.returnToDraft(message, attachments: attachments, id: id)
            await connections[route.hostID]?.refresh(fresh: true)
            throw AgenthailAPIError.unavailable("Steer may not have reached the agent. The message is back in the composer; check the conversation before resending. \(AgenthailAPIError.userMessage(error))")
        }
        await connections[route.hostID]?.refresh(fresh: true)
        await refreshSession(id)
        return true
    }


    /// What was typed into one session or, without one, into every computer
    /// this phone can reach. A computer that cannot answer is reported by name.
    func recentPrompts(sessionID: String?, query: String) async -> (prompts: [PromptItem], problems: [String]) {
        if let sessionID {
            do {
                let (connection, route) = try resolveServer(sessionID)
                let page = try await connection.api.prompts(sessionID: route.sessionID, query: query, limit: 100)
                return (page.prompts.map { qualified($0, as: sessionID) }, [])
            } catch {
                return ([], [AgenthailAPIError.userMessage(error)])
            }
        }
        var prompts: [PromptItem] = []
        var problems: [String] = []
        for host in catalog.hosts {
            do {
                let (api, target) = try reach(host.id)
                let page = try await api.prompts(query: query, limit: 100, host: target.remote)
                prompts += page.prompts.map { qualified($0, as: HostCatalog.qualify(host.id, $0.sessionId)) }
            } catch {
                problems.append("\(host.name): \(AgenthailAPIError.userMessage(error))")
            }
        }
        return (prompts.sorted { ($0.sentAt ?? .distantPast) > ($1.sentAt ?? .distantPast) }, problems)
    }

    private func qualified(_ prompt: PromptItem, as sessionID: String) -> PromptItem {
        var prompt = prompt
        prompt.sessionId = sessionID
        return prompt
    }

    func fork(_ session: SessionState, before prompt: PromptItem? = nil) async throws {
        guard SessionOperationAvailability.fork(session) else { return }
        let turnID = prompt?.turnId
        if prompt != nil && turnID == nil { return }
        let (connection, route) = try resolveServer(session.id)
        let forked = try await connection.api.forkSession(id: route.sessionID, before: turnID, keys: connection.actionKeys)
        let id = HostCatalog.qualify(HostCatalog.owner(of: route.sessionID) ?? route.hostID, forked.id)
        if let prompt { composerSession.setDraft(prompt.text, for: id) }
        await refresh(host: route.hostID)
        openSession(id)
    }

    func operationTarget(_ sessionID: String) -> SessionOperationTarget {
        do {
            let (connection, route) = try resolveServer(sessionID)
            return SessionOperationTarget(hostID: route.hostID, sessionID: route.sessionID, api: connection.api, keys: connection.actionKeys, unavailable: nil)
        } catch {
            return SessionOperationTarget(hostID: nil, sessionID: sessionID, api: nil, keys: ActionRetryKeys(), unavailable: AgenthailAPIError.userMessage(error))
        }
    }

    private func draftKey(for item: QueueState) -> String {
        let known = Set((snapshot?.sessions ?? []).map(\.id))
        let qualified = item.hostID.map { HostCatalog.qualify($0, item.sessionId) }
        return rekeys.resolve(qualified.flatMap { known.contains($0) ? $0 : nil } ?? item.sessionId)
    }

    func sessionOptions(host: String?) async throws -> SessionCreationOptions {
        let (api, target) = try reach(host)
        return try await api.sessionOptions(host: target.remote)
    }
    func creationRootUsage(root: String, host: String?) async throws -> ClaudeRootUsage {
        let (api, target) = try reach(host)
        return try await api.claudeRootUsage(root: root, host: target.remote)
    }
    func creationRootPick(host: String?) async throws -> ClaudeRootPick {
        let (api, target) = try reach(host)
        return try await api.claudeRootPick(host: target.remote)
    }
    func canSetCreationDefaultRoot(host: String?) -> Bool {
        (try? reach(host))?.target.takesSettings == true
    }
    func setCreationDefaultRoot(_ name: String, host: String?) async throws -> ClaudeRootPick {
        let (api, target) = try reach(host)
        guard target.takesSettings else {
            throw AgenthailAPIError.unavailable("Set the default on that computer, or from one paired with it directly.")
        }
        try await api.setClaudeDefaultRoot(name)
        return try await api.claudeRootPick()
    }
    func creationModels(surface: String, host: String?) async throws -> [ModelOption] {
        let (api, target) = try reach(host)
        return try await api.creationModels(surface: surface, host: target.remote)
    }
    func prepareNewSessionForm() {
        creationWarning = nil
        startCreationAgain()
    }
    func startCreationAgain() {
        creationError = nil
        creationRequestIdentity = nil
        creationRequestKey = nil
        creationCanStartAgain = false
    }
    func createSession(surface: String, message: String, cwd: String, model: String, launch: LaunchSettings = .init(), claude: ClaudeCreationSettings = .init(), launcher: LauncherOption? = nil, host: String? = nil) async -> Bool {
        guard !creatingSession, !message.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return false }
        let api: AgenthailAPI
        let target: HostTarget
        do { (api, target) = try self.reach(host) } catch {
            creationError = AgenthailAPIError.userMessage(error)
            return false
        }
        creatingSession = true; creationError = nil; creationWarning = nil; creationCanStartAgain = false
        defer { creatingSession = false }
        let identity = CreationRequestIdentity(surface: surface, message: message, cwd: cwd, model: model,
                                               launch: surface == "codex" ? launch : .init(),
                                               claudeFields: claude.fields, launcher: launcher?.id, host: host)
        if creationRequestIdentity != identity || creationRequestKey == nil {
            creationRequestIdentity = identity
            creationRequestKey = UUID().uuidString
        }
        do {
            let receipt = try await api.createSession(surface: surface, message: message, cwd: cwd, model: model, launch: surface == "codex" ? launch : .init(), claude: claude, launcher: launcher, host: target.remote, idempotencyKey: creationRequestKey, failureReceipts: true)
            if receipt.outcomeUnknown && receipt.id == nil {
                creationError = SessionCreationReceipt.unknownOutcomeMessage
                creationCanStartAgain = true
                return false
            }
            if receipt.ok != true && !receipt.outcomeUnknown && receipt.accepted != true {
                creationError = receipt.error?.message ?? "The session didn't start."
                if receipt.id == nil { creationRequestIdentity = nil; creationRequestKey = nil } else { creationCanStartAgain = true }
                return false
            }
            if receipt.status == "submitted" && receipt.id == nil {
                creationWarning = receipt.warning ?? "Submitted to \(receipt.launcher ?? launcher?.id ?? surface)."
                return false
            }
            creationWarning = receipt.warning
            if let created = receipt.id, !created.isEmpty {
                let id = HostCatalog.qualify(target.remote ?? target.hostID, created)
                let launched = receipt.launcher ?? launcher?.id ?? surface
                deliveryStatus[id] = receipt.status == "submitted" || receipt.outcomeUnknown ? "Submitted to \(launched)." : (surface == "claude" ? "Session started. Waiting for activity." : "Conversation started")
                requestedSessionID = id
            }
            if receipt.id != nil { creationRequestIdentity = nil; creationRequestKey = nil }
            return true
        } catch AgenthailAPIError.outcomeUnknown {
            creationError = SessionCreationReceipt.unknownOutcomeMessage
            creationCanStartAgain = true
            return false
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

    func resume(_ sessionID: String) async {
        do {
            let (connection, route) = try resolveServer(sessionID)
            _ = try await connection.api.sessionLifecycle(sessionID: route.sessionID, action: "resume", keys: connection.actionKeys)
            operationError = nil
        } catch {
            operationError = error.localizedDescription
        }
    }

    /// Stops a session and resumes it, which clears a prompt nothing shows.
    func stopAndResume(_ sessionID: String) {
        guard case let (connection, route)? = try? resolveServer(sessionID) else { return }
        Task {
            do {
                _ = try await connection.api.sessionLifecycle(sessionID: route.sessionID, action: "stop", keys: connection.actionKeys)
                _ = try await connection.api.sessionLifecycle(sessionID: route.sessionID, action: "resume", keys: connection.actionKeys)
                operationError = nil
            } catch {
                operationError = error.localizedDescription
            }
            await refreshSession(sessionID)
        }
    }

    func sentFile(_ item: TimelineItem, sessionID: String) async throws -> URL {
        guard let attachment = item.attachment else { throw AgenthailAPIError.invalidResponse }
        let (connection, route) = try resolveServer(sessionID)
        return try await connection.api.downloadSentFile(sessionID: route.sessionID, attachment: attachment, name: SentFileState.name(item))
    }

    func hostName(forSession sessionID: String) -> String? {
        (try? resolveServer(sessionID))?.connection.name
    }

    func sessionAttachment(for item: TimelineItem, sessionID: String) async -> Data? {
        guard let attachment = item.attachment, selectedSessionID == sessionID, case let (connection, route)? = try? resolveServer(sessionID) else { return nil }
        let cacheKey = "\(sessionID):\(attachment.id)"
        if let cached = attachmentCache[cacheKey] { return cached }
        return await attachmentLoads.value(for: "\(cacheKey)|\(ObjectIdentifier(connection))") { [weak self] in
            guard let data = try? await connection.api.sessionAttachment(sessionID: route.sessionID, id: attachment.id),
                  let self, self.selectedSessionID == sessionID, self.server(for: sessionID) === connection else { return nil }
            if data.count <= self.attachmentCacheLimit {
                if self.attachmentCacheBytes + data.count > self.attachmentCacheLimit {
                    self.attachmentCache.removeAll()
                    self.attachmentCacheBytes = 0
                }
                self.attachmentCache[cacheKey] = data
                self.attachmentCacheBytes += data.count
            }
            return data
        }
    }

    func displayableAttachment(for item: TimelineItem, sessionID: String) async -> Data? {
        guard let data = await sessionAttachment(for: item, sessionID: sessionID) else { return nil }
        guard let attachment = item.attachment, attachment.isImage, SessionAttachment.thumbnail(data, maxPixels: 64) == nil else { return data }
        if let dropped = attachmentCache.removeValue(forKey: "\(sessionID):\(attachment.id)") { attachmentCacheBytes -= dropped.count }
        return nil
    }

    func cachedAttachment(for item: TimelineItem, sessionID: String) -> Data? {
        item.attachment.flatMap { attachmentCache["\(sessionID):\($0.id)"] }
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
                await push.beginRepair(hostID)
                if let previous {
                    if let relayURL = previous.pushRelayURL, let failure = await push.revokeEarlierRegistration(hostID, relayURL: relayURL) {
                        try? await pairedAPI.revokeCurrentDevice()
                        throw AgenthailAPIError.unavailable(failure)
                    }
                    if !previous.isPreview {
                        try? await previous.api.removePush()
                        try? await previous.api.revokeCurrentDevice()
                    }
                    previous.stop()
                    push.clearStoredPush(hostID)
                    store.prune { $0.hasPrefix(hostID + HostCatalog.separator) }
                }
                let credential = PairedHostCredential(hostID: hostID, hostName: response.host.name, endpoint: pendingPairing.endpoint, token: response.token, pairedAt: previous == nil ? Date() : PairedCredential.all().first { $0.hostID == hostID }?.pairedAt ?? Date())
                try PairedCredential.save(credential)
                add(HostConnection(credential: credential, session: networkSession, drops: connectionDrops))
                push.finishRepair(hostID)
                attachmentCache.removeAll()
                attachmentCacheBytes = 0
                pairing = false
                operationError = nil
                completedPairings += 1
                rebuild()
                if automaticallyConnect {
                    connections[hostID]?.connect()
                    push.requestNotifications()
                }
            } catch {
                if let newAPI { try? await newAPI.revokeCurrentDevice() }
                pairing = false
                operationError = error.localizedDescription
                push.abandonRepair(pendingPairing.hostID)
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
        composerSession.retryComposerStore()
        guard isPaired else {
            push.refreshNotificationRegistration()
            return
        }
        push.showNotificationAuthorization()
        connect()
        composerSession.wake()
    }

    @discardableResult
    func refresh(fresh: Bool = false) async -> Bool {
        await HostRefresh.all(hostOrder.compactMap { connections[$0] }, fresh: fresh, within: hostRefreshWait)
    }

    func refresh(host hostID: String) async {
        _ = await connections[hostID]?.refresh(fresh: true)
    }

    var listedHosts: [HostConnection] { hostOrder.compactMap { connections[$0] } }

    func listedHost(_ hostID: String) -> HostConnection? { connections[hostID] }

    func projectSessions(_ project: ListedProject) -> [SessionState] {
        let ids = projectSessionIDs[ProjectContents.key(of: project)] ?? []
        let lead = project.state.leads.first.flatMap { ProjectContents.leadSessionID($0, of: project) }
        return (lead.map { ids.contains($0) ? ids : ids + [$0] } ?? ids).compactMap(session)
    }

    func mentionSessions(for sessionID: String, query: String) async -> [SessionState]? {
        guard let host = server(for: sessionID), let rows = try? await host.mentions(query) else { return nil }
        return rows.map { HostCatalog.merge($0, servedBy: host.id, ownerName: { id in self.catalog.hosts.first { $0.id == id }?.name }, reachable: true).row }
    }

    func loadSession(_ id: String) async {
        let id = rekeys.resolve(id)
        let switching = selectedSessionID != id
        if let previous = selectedSessionID, previous != id {
            composerSession.leave(previous)
            keepSelectedSession()
        }
        var resumes = false
        if selectedSessionID != id {
            sessionStreamTask?.cancel()
            cancelSessionReload()
            unsupportedRefreshTask?.cancel()
            sessionMetadataTask?.cancel()
            sessionStreamCursor = 0
            streamOutranPage = false
            sessionStreamUnsupported = false
            composerSession.enter(id)
            let warm = store.open(id)
            sessionMetadata = MetadataOverlay(allClaudeSubagents: warm?.allClaudeSubagents ?? false)
            let live = snapshot?.sessions.first { $0.id == id }
            selectedDetail = warm.map { SessionStore.live($0.detail, live) }
            if sessionRequests != nil { sessionRequests = nil }
            sessionRequestsKey = nil
            olderActivity = warm?.older ?? []
            activityCursor = warm?.olderCursor
            olderActivityError = nil
            if let cursor = warm?.cursor, live?.capabilities.stream == true {
                sessionStreamCursor = cursor
                resumes = true
            }
        }
        selectedSessionID = id
        if switching { beginOpenInterval(id, resumes: resumes) }
        sessionError = nil
        loadingSession = selectedDetail == nil
        loadCommands(id)
        if resumes {
            sessionServer = server(for: id)
            startSessionStream(id)
            await refreshDeliveries()
        } else {
            await reloadSelectedSession()
        }
        if selectedSessionID == id { startSessionMetadata(id) }
        if selectedSessionID == id { loadingSession = false }
    }

    /// Marks a session open in Instruments, from the switch until the
    /// daemon's first page for it is shown (`loaded`), its stream resumes from
    /// the cached position (`resumed`), or the selection moves off it
    /// (`left`). `Session painted` marks the first frame drawn after its rows
    /// are shown.
    private func beginOpenInterval(_ id: String, resumes: Bool) {
        let signpost = Self.signposter.makeSignpostID()
        let cached = selectedDetail?.session.id == id
        openInterval = (signpost, Self.signposter.beginInterval("Session open", id: signpost, "cached \(cached)"), id, false, resumes)
        paintOpeningSession()
    }

    private func endOpenInterval(_ outcome: String) {
        guard let openInterval else { return }
        Self.signposter.endInterval("Session open", openInterval.state, "\(outcome, privacy: .public)")
        self.openInterval = nil
    }

    private func paintOpeningSession() {
        guard let open = openInterval, !open.painted, selectedDetail?.session.id == open.session else { return }
        openInterval?.painted = true
        _ = NextFrame { Self.signposter.emitEvent("Session painted", id: open.id) }
    }

    /// Files an `@` in this session's draft can name, from the computer that
    /// runs it; nil when that computer can't be asked.
    func sessionFiles(_ id: String, query: String) async -> [SessionFile]? {
        guard case let (connection, route)? = try? resolveServer(id) else { return nil }
        let files = try? await connection.api.sessionFiles(id: route.sessionID, query: query).files
        return server(for: id) === connection ? files : nil
    }

    private func loadCommands(_ id: String) {
        guard case let (connection, route)? = try? resolveServer(id), let read = store.beginCommandsRead(id) else { return }
        Task { [weak self, store] in
            do {
                let commands = try await connection.api.sessionCommands(id: route.sessionID)
                guard self?.server(for: id) === connection else {
                    store.commandsRead(nil, for: id, read: read, retry: true)
                    return
                }
                store.commandsRead(commands, for: id, read: read)
            } catch {
                var olderHost = false
                if case AgenthailAPIError.olderHost = error { olderHost = true }
                store.commandsRead(nil, for: id, read: read, retry: !olderHost && !(error is CancellationError))
            }
        }
    }

    /// A stream resumed from a cached position brings the shown rows current
    /// without a page read.
    private func streamConnected(_ id: String) {
        guard selectedSessionID == id, pageReadsInFlight == 0, let open = openInterval, open.session == id, open.resumes else { return }
        endOpenInterval("resumed")
    }

    private func keepSelectedSession() {
        guard let id = selectedSessionID, let detail = selectedDetail, detail.session.id == id else { return }
        let cursor = sessionStreamCursor > 0 && !streamOutranPage && detail.timeline != nil ? sessionStreamCursor : nil
        store.keep(WarmSession(detail: detail, older: olderActivity, olderCursor: activityCursor, cursor: cursor, allClaudeSubagents: sessionMetadata.allClaudeSubagents), for: id)
    }

    func refreshSession(_ id: String) async {
        guard sessionLoadIsCurrent(id, selectedID: selectedSessionID) else { return }
        if openInterval?.session == id { openInterval?.resumes = false }
        let requestID = UUID()
        sessionRequestID = requestID
        let seq = nextStateRequest()
        let requestServer = server(for: id)
        pageReadsInFlight += 1
        defer {
            pageReadsInFlight -= 1
            if pageReadsInFlight == 0 { rowsDuringPageRead = [] }
        }
        do {
            let (connection, route) = try resolveServer(id)
            var detail = try await connection.api.sessionDetail(id: route.sessionID, includeTimeline: true, waitForCatchUp: false)
            detail.session.id = id
            guard sessionLoadIsCurrent(id, selectedID: selectedSessionID), sessionRequestID == requestID, server(for: id) === connection else { return }
            if !olderActivity.isEmpty {
                let fresh = Set(detail.timeline?.items.map(\.id) ?? [])
                let retained = fresh.union(olderActivity.map(\.id))
                let shown = selectedDetail?.timeline?.items ?? []
                // Only rows before the page's start fell off it; later rows it lacks are newer than it.
                let shared = shown.firstIndex { fresh.contains($0.id) } ?? shown.count
                let fellOff = shown[..<shared].filter { !retained.contains($0.id) }
                if !fellOff.isEmpty { olderActivity += fellOff }
            }
            var merged = sessionMetadata.apply(to: detail)
            if seq < selectedSessionStateSeq, let current = selectedDetail, current.session.id == id {
                merged.liveState = current.liveState
            } else {
                selectedSessionStateSeq = seq
            }
            selectedDetail = merged
            streamOutranPage = false
            if let journalSeq = detail.journalSeq {
                sessionStreamCursor = journalSeq
                applySessionStreamEvents(rowsDuringPageRead.filter { $0.seq > journalSeq }, route: route, server: connection, replaying: true)
            }
            if olderActivity.isEmpty { activityCursor = detail.timeline?.nextBefore }
            sessionError = nil
            if openInterval?.session == id { endOpenInterval("loaded") }
        } catch {
            guard sessionLoadIsCurrent(id, selectedID: selectedSessionID), sessionRequestID == requestID, server(for: id) === requestServer else { return }
            sessionError = error.localizedDescription
            if let openInterval, openInterval.session == id { Self.signposter.emitEvent("Session open failed", id: openInterval.id) }
        }
        await refreshDeliveries()
    }

    func settleRetiredAsks(_ session: SessionState) {
        if retiredAsks.settles(session) { retiredAsks.settle(session) }
    }

    func syncRequests(for session: SessionState) async {
        settleRetiredAsks(session)
        guard selectedSessionID == session.id else { return }
        guard RequestSync.lists(session) else {
            if sessionRequests != nil { sessionRequests = nil }
            sessionRequestsKey = nil
            return
        }
        let key = RequestSync.cacheKey(session)
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
            if sessionRequests != requests { sessionRequests = requests }
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
        var failure: RequestAnswerFailure?
        do {
            let (api, route) = try resolve(sessionID)
            _ = try await api.respond(sessionID: route.sessionID, requestID: request.id, decision: decision)
            operationError = nil
            composerSession.refusedSendDeleted(ask: request.id, choice: decision.choices.first)
        } catch {
            failure = RequestAnswerFailure(error)
            if failure?.dropsCard == true { sessionRequests = sessionRequests?.removing(request.id) }
            operationError = failure?.message
        }
        if RequestAnswerFailure.retires(request, failure: failure) {
            retiredAsks.retire(request.id, in: sessionID, receipt: failure == nil ? AnswerReceipt(request, decision) : nil)
        }
        await fetchRequests(sessionID, key: sessionRequestsKey)
        await refreshSession(sessionID)
    }

    func withdraw(_ request: SessionRequest, sessionID: String) async {
        guard answeringRequest == nil else { return }
        answeringRequest = request.id
        defer { answeringRequest = nil }
        do {
            let (api, route) = try resolve(sessionID)
            try await api.withdrawRequest(sessionID: route.sessionID, requestID: request.id)
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

    func setTurnSetting(_ key: String, to value: String, sessionID: String) async {
        guard !pendingControls.contains(sessionID) else { return }
        pendingControls.insert(sessionID)
        defer { pendingControls.remove(sessionID) }
        do {
            let (api, route) = try resolve(sessionID)
            try await api.setTurnSetting(sessionID: route.sessionID, key: key, value: value)
            operationError = nil
        } catch {
            operationError = AgenthailAPIError.userMessage(error)
            return
        }
        if selectedSessionID == sessionID { startSessionMetadata(sessionID) }
        if key == TurnSetting.mode { await fetchRequests(sessionID, key: sessionRequestsKey) }
    }

    // A send to a computer reached through another is queued on the owner, or on the other computer while the owner is down.
    func queuedDelivery(id: Int64, sessionID: String) async -> QueueState? {
        guard let route = catalog.route(for: sessionID), let connection = connections[route.hostID] else { return nil }
        let owner = HostCatalog.owner(of: sessionID) ?? route.hostID
        let expected = HostCatalog.qualify(route.hostID, route.sessionID)
        for host in owner == route.hostID ? [nil] : [owner, nil] {
            let found = try? await connection.api.queueItem(id: id, host: host, timeout: InboxPage.hostTimeout)
            if let item = found.flatMap({ $0 }).map({ InboxPage.qualified($0, host: owner) }), item.sessionId == expected { return item }
        }
        return nil
    }

    private func refreshDeliveries() async {
        guard !deliveryQueueIDs.isEmpty, !refreshingDeliveries else { return }
        refreshingDeliveries = true
        defer { refreshingDeliveries = false }
        let expected = deliveryQueueIDs
        for (sessionID, queueID) in expected where deliveryQueueIDs[sessionID] == queueID {
            guard let item = await queuedDelivery(id: queueID, sessionID: sessionID) else { continue }
            switch item.isHistorical && item.evidence == "unknown" ? "unknown-history" : item.isHistorical && item.evidence == "failed" ? "failed-history" : item.evidence {
            case "submitted": deliveryStatus[sessionID] = "Submitted to \(item.target)."
            case "queued": deliveryStatus[sessionID] = item.queuedReceipt
            case "unknown": deliveryStatus[sessionID] = "Submitted to \(item.target)."
            case "failed": deliveryStatus[sessionID] = "Delivery failed. Review it in Inbox before retrying."
            case "unknown-history": deliveryStatus[sessionID] = "Delivery was never confirmed. Review it in Inbox history."
            case "failed-history": deliveryStatus[sessionID] = "Delivery failed. Review it in Inbox history."
            case "transport_accepted": deliveryStatus[sessionID] = "Sent to \(item.target)."
            case "delivered": deliveryStatus[sessionID] = "Sent to \(item.target)."
            case "held": deliveryStatus[sessionID] = "Held by the receiver."
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
        let allSubagents = sessionMetadata.allClaudeSubagents
        sessionMetadataTask = Task { [weak self, connection] in
            do {
                let metadata = try await connection.api.sessionMetadata(id: route.sessionID, allSubagents: allSubagents)
                guard !Task.isCancelled, self?.server(for: id) === connection else { return }
                self?.updateSessionMetadata(for: id) { $0.absorb(metadata) }
            } catch {
                guard !Task.isCancelled, self?.server(for: id) === connection else { return }
                self?.updateSessionMetadata(for: id) { $0.metadataFailed() }
            }
        }
    }

    var showsAllClaudeSubagents: Bool { sessionMetadata.allClaudeSubagents }

    func showAllClaudeSubagents() {
        guard let id = selectedSessionID else { return }
        sessionMetadata.showAllClaudeSubagents()
        store.update(id) { $0.allClaudeSubagents = true }
        startSessionMetadata(id)
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
        let requestServer = server(for: id)
        do {
            let (connection, route) = try resolveServer(id)
            let page = try await connection.api.sessionDetail(id: route.sessionID, includeTimeline: true, timelineBefore: cursor, waitForCatchUp: false)
            guard selectedSessionID == id, server(for: id) === connection, activityCursor == cursor else { return }
            guard let timeline = page.timeline, timeline.unavailableReason == nil else {
                throw AgenthailAPIError.unavailable(page.timeline?.unavailableReason ?? "Older activity is unavailable.")
            }
            let known = Set(olderActivity.map(\.id) + (selectedDetail?.timeline?.items.map(\.id) ?? []))
            let earlier = timeline.items.filter { !known.contains($0.id) }
            if !earlier.isEmpty { olderActivity = earlier + olderActivity }
            activityCursor = timeline.nextBefore
            olderActivityError = nil
        } catch {
            if selectedSessionID == id, activityCursor == cursor, server(for: id) === requestServer {
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
        let reachable = hostOrder.compactMap { connections[$0] }.filter(\.shownReachable)
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

    func openSession(_ sessionID: String, turn: String? = nil) {
        let resolved = rekeys.resolve(sessionID)
        if let turn { openedTurns[resolved] = turn }
        routeOpenings += 1
        requestedSessionID = resolved
    }

    private(set) var openedTurns: [String: String] = [:]

    func takeOpenedTurn(_ sessionID: String) -> String? {
        openedTurns.removeValue(forKey: rekeys.resolve(sessionID))
    }

    private func followRekeys(_ retired: [String: String]) {
        rekeys.record(retired)
        guard !rekeys.isEmpty else { return }
        composerSession.carry(rekeys)
        store.carry(rekeys)
        _ = rekeys.carry(&openedTurns)
        if let requested = requestedSessionID, rekeys.resolve(requested) != requested {
            requestedSessionID = rekeys.resolve(requested)
        } else if let selected = selectedSessionID, rekeys.resolve(selected) != selected {
            requestedSessionID = rekeys.resolve(selected)
        }
    }


    func pickHolder(_ holder: SessionHolder?, for sessionID: String) {
        HolderPins.shared.pick(holder.map(HolderPin.init), for: sessionID)
        let picks = HolderPins.shared.all
        if holderPicks != picks { holderPicks = picks }
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

    func moveToAccount(_ session: SessionState, root: String) {
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
                try await api.moveToAccount(sessionID: route.sessionID, root: root)
                await refresh(host: route.hostID)
                await refreshSession(session.id)
            } catch {
                operationError = AgenthailAPIError.userMessage(error)
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

    func setPinned(sessionID: String, _ pinned: Bool) async {
        do {
            let (api, route) = try resolve(sessionID)
            try await api.action(pinned ? "session-pin" : "session-unpin", sessionID: route.sessionID)
            operationError = nil
            await refresh(host: route.hostID)
        } catch {
            operationError = error.localizedDescription
        }
    }

    func removeHost(_ hostID: String) {
        guard let connection = connections[hostID] else { return }
        let relay = connection.pushRelayURL.map { PushRelayClient(baseURL: $0, session: networkSession) }
        let stored = push.storedRegistration(hostID)
        Task {
            do {
                try? await connection.api.removePush()
                try await connection.api.revokeCurrentDevice()
                if let relay, let stored { try? await relay.revoke(stored) }
                clearHost(hostID)
            } catch {
                operationError = nil
                forgetCandidate = hosts.first { $0.id == hostID }
            }
        }
    }

    func forgetHost(_ hostID: String) {
        let relay = connections[hostID]?.pushRelayURL.map { PushRelayClient(baseURL: $0, session: networkSession) }
        let stored = push.storedRegistration(hostID)
        clearHost(hostID)
        forgetCandidate = nil
        Task {
            if let relay, let stored { try? await relay.revoke(stored) }
        }
    }

    private func clearHost(_ hostID: String) {
        connections[hostID]?.stop()
        push.stopSetup(hostID)
        connections.removeValue(forKey: hostID)
        extensionCatalogs.removeValue(forKey: hostID)
        let wasAppearanceHost = hostOrder.first == hostID
        hostOrder.removeAll { $0 == hostID }
        if wasAppearanceHost, let next = hostOrder.first.flatMap({ connections[$0] }), next.reachable { appearance.load(from: next.api) }
        KeychainStore.remove(PairedCredential.account(for: hostID))
        KeychainStore.remove(HostConnection.relayAccount(hostID))
        PushReceipts.forget(host: hostID)
        connectionDrops?.forget(host: hostID)
        push.forget(hostID)
        let prefix = hostID + HostCatalog.separator
        func owned(_ id: String) -> Bool { id.hasPrefix(prefix) }
        if let selected = selectedSessionID, owned(selected) {
            sessionStreamTask?.cancel()
            cancelSessionReload()
            unsupportedRefreshTask?.cancel()
            sessionMetadataTask?.cancel()
            selectedDetail = nil
            selectedSessionID = nil
            composerSession.clearComposer()
        }
        composerSession.forget(where: owned)
        store.prune(removing: owned)
        deliveryStatus = deliveryStatus.filter { !owned($0.key) }
        deliveryQueueIDs = deliveryQueueIDs.filter { !owned($0.key) }
        searchResults = searchResults.filter { !owned($0.id) }
        attachmentCache.removeAll()
        attachmentCacheBytes = 0
        creationRequestIdentity = nil
        creationRequestKey = nil
        rebuild()
        push.updateNotificationStatus()
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


    func setAppActive(_ active: Bool) {
        if active, let since = inactiveSince {
            inactiveSince = nil
            // Contact from before a long absence says nothing about the network now, so the first failure after it is shown at once.
            for connection in connections.values where Date().timeIntervalSince(since) >= connection.grace.window { connection.grace.forget() }
        } else if !active, inactiveSince == nil {
            inactiveSince = Date()
        }
        push.setAppActive(active)
    }

    private func startSessionStream(_ id: String) {
        sessionStreamTask?.cancel()
        guard selectedSessionID == id, case let (connection, route)? = try? resolveServer(id) else { return }
        sessionStreamTask = Task {
            let backoff = EventRetryBackoff()
            let batch = StreamBatch<SessionStreamEvent>(interval: Self.streamPublishInterval) { [weak self] events in
                self?.applySessionStreamEvents(events, route: route, server: connection)
            }
            while !Task.isCancelled, selectedSessionID == id, server(for: id) === connection {
                do {
                    try await connection.api.streamSession(id: route.sessionID, after: sessionStreamCursor, onConnected: { [weak self] in await self?.streamConnected(id) }, onEvent: { event in
                        backoff.reset()
                        batch.add(event)
                    })
                } catch {
                    batch.flush()
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
        let state = SessionLiveState(live, lastActive: detail.session.lastActive)
        guard detail.liveState != state else { return }
        detail.liveState = state
        selectedDetail = detail
    }

    func applySessionStreamEvent(_ event: SessionStreamEvent) {
        guard let id = selectedSessionID, let route = catalog.route(for: id), let connection = connections[route.hostID] else { return }
        applySessionStreamEvents([event], route: route, server: connection)
    }

    private func applySessionStreamEvents(_ events: [SessionStreamEvent], route: SessionRoute, server connection: HostConnection, replaying: Bool = false) {
        guard let selected = selectedSessionID, catalog.route(for: selected) == route, server(for: selected) === connection,
              var detail = selectedDetail, detail.session.id == selected else { return }
        var older = olderActivity
        var error = sessionError
        var changed = false
        var textOnly = true
        for event in events where event.stream == "session" && event.sessionId == route.sessionID {
            if pageReadsInFlight > 0, !replaying { rowsDuringPageRead.append(event) }
            sessionStreamCursor = max(sessionStreamCursor, event.seq)
            switch event.data.kind {
            case "commands":
                store.commandsStreamed(RuntimeCommands(commands: event.data.commands ?? [], unavailable: event.data.unavailable ?? ""), for: selected)
            case "context":
                sessionMetadata.stream(context: event.data.context)
                detail.context = event.data.context
                changed = true
                textOnly = false
            case "goal":
                sessionMetadata.stream(goal: event.data.goal)
                detail.goal = event.data.goal
                changed = true
                textOnly = false
            case "source-error" where event.data.op == "reset":
                error = event.data.reason ?? "The session source restarted."
                streamOutranPage = true
            default:
                guard var timeline = detail.timeline, !event.data.itemId.isEmpty else { continue }
                if event.data.op == "remove" {
                    timeline.items.removeAll { $0.id == event.data.itemId }
                    older.removeAll { $0.id == event.data.itemId }
                    textOnly = false
                } else {
                    let item = Self.timelineItem(event)
                    if !(timeline.items.first { $0.id == item.id }.map { Self.onlyTextGrew(from: $0, to: item) } ?? false) { textOnly = false }
                    SessionStore.place(item, in: &timeline, older: &older)
                }
                detail.timeline = timeline
                error = nil
                changed = true
            }
        }
        if older != olderActivity { olderActivity = older }
#if DEBUG
        BodyRuns.hit(!changed ? "streamBatchUnchanged" : textOnly ? "streamBatchText" : "streamBatchDetail")
#endif
        if changed, textOnly { streamText(into: detail) } else if changed { selectedDetail = detail }
        if error != sessionError { sessionError = error }
        if changed, !replaying, snapshot?.sessions.contains(where: { $0.id == selected }) != true { readUnlistedState(selected) }
    }

    // A session missing from the snapshot gets no status from it, so its own stream prompts a read.
    private func readUnlistedState(_ id: String) {
        guard unlistedStateRead == nil else {
            unlistedStatePending = id
            return
        }
        unlistedStateRead = Task { [weak self] in
            await self?.readSelectedState(id)
            guard let self else { return }
            self.unlistedStateRead = nil
            if let next = self.unlistedStatePending {
                self.unlistedStatePending = nil
                self.readUnlistedState(next)
            }
        }
    }

    private func readSelectedState(_ id: String) async {
        guard selectedSessionID == id, snapshot?.sessions.contains(where: { $0.id == id }) != true,
              case let (connection, route)? = try? resolveServer(id) else { return }
        let seq = nextStateRequest()
        guard let read = try? await connection.api.sessionDetail(id: route.sessionID, includeTimeline: false, waitForCatchUp: false),
              selectedSessionID == id, server(for: id) === connection, seq > selectedSessionStateSeq,
              var detail = selectedDetail, detail.session.id == id else { return }
        selectedSessionStateSeq = seq
        guard detail.liveState != read.liveState else { return }
        detail.liveState = read.liveState
        selectedDetail = detail
    }

    static func onlyTextGrew(from old: TimelineItem, to new: TimelineItem) -> Bool {
        guard old.kind == "message", old.role == "assistant", old.agent == nil else { return false }
        var grown = old
        grown.text = new.text
        grown.truncated = new.truncated
        grown.timestamp = new.timestamp
        return grown == new
    }

    private static func timelineItem(_ event: SessionStreamEvent) -> TimelineItem {
        event.item ?? TimelineItem(id: event.data.itemId, kind: event.data.kind, role: event.data.role, title: event.data.title ?? (event.data.kind == "attachment" ? "Image" : event.data.kind), text: event.data.body ?? (event.data.kind == "attachment" ? "Image attachment" : ""), timestamp: event.data.ts, callId: event.data.callId, status: event.data.status, truncated: event.data.truncated, truncationReason: event.data.truncationReason, bodyRef: event.data.bodyRef, attachment: event.data.attachment, sender: event.data.sender, files: event.data.files, resetsAt: event.data.resetsAt, turnId: event.data.turnId)
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
        if let catalog = extensionCatalogs[host.id] {
            catalog.attach(host.api, hostID: host.id, hostName: host.name)
            if host.reachable && (catalog.wanted || ExtensionPins.shared.pins(host: host.id)) && (catalog.list == nil || catalog.error != nil) && !catalog.loading { catalog.refresh() }
        }
        if !host.reachable { push.hostUnreachable(host.id) }
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
        if host.id == hostOrder.first { appearance.load(from: host.api) }
        push.hostConnected(host)
#if DEBUG
        if LiveTestHosts.alertsOnConnect() { operationError = "\(host.name): \(LiveTestHosts.connectAlert)" }
#endif
    }

    func hostAppearanceChanged(_ host: HostConnection) {
        guard connections[host.id] === host, host.id == hostOrder.first else { return }
        appearance.load(from: host.api)
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
        let removed = HostCatalog.qualify(host.id, id)
        fetchedSubagents.sessionRemoved(removed, unlisted: unlisted)
        if !unlisted { store.prune { $0 == removed } }
    }

}

/// Runs once on the next display frame, which starts after the current
/// run loop pass has committed its view updates.
private final class NextFrame: NSObject {
    private let action: () -> Void
    private var link: CADisplayLink?

    init(_ action: @escaping () -> Void) {
        self.action = action
        super.init()
        link = CADisplayLink(target: self, selector: #selector(fire))
        link?.add(to: .main, forMode: .common)
    }

    @objc private func fire() {
        link?.invalidate()
        link = nil
        action()
    }
}
