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
final class AgenthailIOSModel: HostConnectionDelegate {
    static let voiceHostKey = "voiceHost"
    @ObservationIgnored var hostRefreshWait: Duration = HostRefresh.wait

    private(set) var snapshot: DashboardSnapshot? {
        didSet {
            cells.adopt(snapshot?.sessions ?? [])
            daemon = snapshot?.daemon
            subagentIDs = (snapshot?.sessions ?? []).reduce(into: [:]) { ids, row in
                if let parent = row.subagent?.parentId { ids[parent, default: []].append(row.id) }
            }
            sessionAttention = SessionAttention.build(sessions: snapshot?.sessions ?? [], attention: snapshot?.attention ?? [], deliveryProblems: snapshot?.deliveryProblems ?? [])
            rederiveTree()
            if let snapshot {
                HolderPins.shared.prune(to: snapshot.sessions)
                let picks = HolderPins.shared.all
                if holderPicks != picks { holderPicks = picks }
            }
        }
    }
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
    @ObservationIgnored private var listedTree: (key: ListKey, tree: SessionTree)?
    private(set) var listRevision: UInt64 = 0
    @ObservationIgnored private var orderHold = OrderHold()
    @ObservationIgnored private var heldList: ScopePage.View?
    @ObservationIgnored private var heldKey: ListKey?
    @ObservationIgnored private var heldCells: [String: SessionCell] = [:]
    @ObservationIgnored private var shownOrder: [ScopePage.View?] = []
    var selectedDetail: SessionDetail? {
        didSet { paintOpeningSession() }
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
    var pairing = false
    private(set) var completedPairings = 0
    let draft = ComposerDraft()
    let appearance = PhoneAppearance()
    var composer: String {
        get { draft.text }
        set { draft.text = newValue }
    }
    var sessionError: String?
    var loadingSession = false
    var sendingSessionIDs: Set<String> = []
    private static let signposter = OSSignposter(subsystem: "com.agenthail.ios", category: "SessionOpen")
    private static let composerLog = Logger(subsystem: "com.agenthail.ios", category: "Composer")
    @ObservationIgnored private var openInterval: (id: OSSignpostID, state: OSSignpostIntervalState, session: String, painted: Bool, resumes: Bool)?
    private var attachmentCache: [String: Data] = [:]
    @ObservationIgnored private var attachmentCacheBytes = 0
    private let attachmentCacheLimit = 20 * 1024 * 1024
    private let attachmentLoads = SharedLoads<Data?>()
    var deliveryStatus: [String: String] = [:]
    var controlOutput: [String: String] = [:]
    var deliveryIDs: [String: Int64] = [:]
    @ObservationIgnored private var deliveryQueueIDs: [String: Int64] = [:]
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
    private static let composerFileKeptNotice = "Saved drafts couldn’t be loaded. Their file was kept on this iPhone."
    private static let composerUnreadNotice = "Saved drafts couldn’t be read. Until they can, drafts and unconfirmed messages are lost if Agenthail closes."
    @ObservationIgnored private var composerWaitAnnounced = false
    @ObservationIgnored private var composerSave: Task<Void, Never>?
    @ObservationIgnored private var draftChanges: AnyCancellable?
    var composerAttachments: [ComposerAttachment] = [] {
        didSet { saveComposerStateSoon() }
    }
    var attachmentProblem: String?
    private var attachmentDrafts: [String: [ComposerAttachment]] = [:] {
        didSet { saveComposerStateSoon() }
    }
    private var rekeys = SessionRekeys()
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
    private struct StoredPush: Codable {
        let registration: PushRegistration
        let deviceToken: String
        let environment: String
        let endpoint: String
    }
    @ObservationIgnored private var creationRequestIdentity: CreationRequestIdentity?
    @ObservationIgnored private var creationRequestKey: String?
    var notificationStatus = "Not enabled"
    var requestedSessionID: String?
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
    @ObservationIgnored private var connections: [String: HostConnection] = [:]
    private var hostOrder: [String] = []
    @ObservationIgnored private var catalog = HostCatalog()
    private var routing = HostCatalog.Routing()
    private(set) var isPaired = false
    private(set) var showsHostNames = false
    private(set) var usageReaders: [AgenthailAPI] = []
    private(set) var listing = ListingState()
    private(set) var decisionsKey = ""
    private(set) var queue: [QueueState] = []
    private(set) var attention: [AttentionState] = []
    private(set) var deliveryProblems: [DeliveryProblem] = []
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
        store.pinned = { [unowned self] id in id == selectedSessionID || drafts[id]?.isEmpty == false }
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
    @ObservationIgnored private var pushDeviceToken: String?
    @ObservationIgnored private var pushStatus: [String: String] = [:]
    @ObservationIgnored private var pushRetries: [String: (id: UUID, connection: ObjectIdentifier, task: Task<Void, Never>)] = [:]
    @ObservationIgnored private var pushFailures: [String: String] = [:]
    @ObservationIgnored private var deviceRegistrationFailure: String?
    @ObservationIgnored private var appActive = true
    @ObservationIgnored private var repairingPush: Set<String> = []
    @ObservationIgnored private let notificationDefaults: UserDefaults
    private static let notificationsOffKey = "notificationsTurnedOff"
    private var pushTurnedOff: Bool {
        get { notificationDefaults.bool(forKey: Self.notificationsOffKey) }
        set { notificationDefaults.set(newValue, forKey: Self.notificationsOffKey) }
    }
    var notificationProblems: [String] = []
    private let networkSession: URLSession
    private let connectionDrops: ConnectionDrops?
    private let automaticallyConnect: Bool
    private let previewPaired: Bool

    func session(_ id: String?) -> SessionState? { cells.session(id) }
    func pairedHost(owning sessionID: String) -> SessionHost? { daemon?.pairedHost(owning: sessionID) }

    /// Names the lead that replaced a request's session; the computer serving the request gives the id unqualified.
    func leadName(_ id: String, requestFrom sessionID: String) -> String {
        let qualified = HostCatalog.qualify(routing.route(for: sessionID)?.hostID ?? HostCatalog.owner(of: sessionID) ?? "", id)
        return session(qualified)?.displayName ?? id
    }

    init(autoConnect: Bool = true, session: URLSession = .shared, drops: ConnectionDrops? = .shared, composerStore: ComposerStore? = nil, notificationDefaults: UserDefaults = .standard) {
        self.notificationDefaults = notificationDefaults
        networkSession = session
        connectionDrops = drops
        automaticallyConnect = autoConnect
        previewPaired = false
        self.composerStore = composerStore
        voiceHostID = UserDefaults.standard.string(forKey: Self.voiceHostKey)
        for credential in PairedCredential.all() { add(HostConnection(credential: credential, session: session, drops: drops)) }
        rebuild()
        connectSendQueue()
        restoreComposerState()
#if DEBUG
        for hostID in LiveTestHosts.loopbackHostIDs() { clearHost(hostID) }
#endif
        Task { await retireLegacyPairing() }
        if autoConnect { connect() }
    }

    init(api: AgenthailAPI, paired: Bool = false, hostID: String = "computer", hostName: String = "computer", composerStore: ComposerStore? = nil) {
        notificationDefaults = .standard
        networkSession = .shared
        connectionDrops = nil
        automaticallyConnect = false
        previewPaired = paired
        self.composerStore = composerStore
        add(HostConnection(id: hostID, name: hostName, api: api))
        rebuild()
        connectSendQueue()
        restoreComposerState()
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
                channels: [], relays: [], history: [], attention: catalog.attention,
                deliveryProblems: catalog.deliveryProblems,
                codexRecentHours: loaded.first?.codexRecentHours ?? 24, busyDelivery: nil,
                leadReplacements: catalog.leadReplacements)
            if snapshot?.hasSamePresentation(as: merged) != true { snapshot = merged }
        }
        let order = orderSignature
        if order != shownOrder {
            shownOrder = order
            rederiveTree()
        }
        store.carry(rekeys)
        hosts = ordered.map { connection in
            PairedHostStatus(id: connection.id, name: connection.name, address: connection.credential.endpoint.host ?? "",
                             reachable: connection.reachable, reconnecting: connection.reconnecting, error: connection.connectionError,
                             surfaces: (connection.snapshot?.surfaces ?? []).filter(\.needsAttention), signin: connection.snapshot?.signin ?? [],
                             peers: connection.snapshot?.daemon.hosts ?? [], notifications: pushStatus[connection.id] ?? "Not enabled",
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
        let failing = ordered.filter { $0.connectionError != nil }
        if failing.isEmpty { connectionError = nil }
        else if ordered.count == 1 { connectionError = failing[0].connectionError }
        else { connectionError = failing.map { "\($0.name): \($0.connectionError ?? "")" }.joined(separator: "\n") }
        reconnecting = ordered.contains(where: \.reconnecting)
        deriveParts(ordered)
        if let voice = voiceHostID, connections[voice] == nil { voiceHostID = nil }
        rebindSelectedSession()
        refetchMovedSubagents()
    }

    private func deriveParts(_ ordered: [HostConnection]) {
        let sessions = snapshot?.sessions ?? []
        routing = catalog.routing
        isPaired = !hostOrder.isEmpty && (previewPaired || connections.values.contains { !$0.isPreview })
        showsHostNames = Set(sessions.compactMap(\.host?.id)).count > 1 || hostOrder.count > 1
        let readers = ordered.filter(\.reachable).map(\.api)
        if !usageReaders.elementsEqual(readers, by: ===) { usageReaders = readers }
        listing = ListingState.read(listedHosts, projects: projects, earlier: earlierTargets)
        let reached = catalog.hosts.map { host in "\(host.id):\(host.state):\(catalog.target(forHost: host.id).map { "\($0.hostID)>\($0.remote ?? "")" } ?? "none")" }
        decisionsKey = (ordered.map { "\($0.id):\($0.shownReachable):\($0.snapshot?.decisionsRev ?? "older")" } + reached).joined(separator: ",")
        queue = snapshot?.queue ?? []
        attention = snapshot?.attention ?? []
        deliveryProblems = snapshot?.deliveryProblems ?? []
        leadReplacements = snapshot?.leadReplacements ?? []
        waitingSessions = sessions.filter { $0.waitingOn != nil || $0.pendingAskID != nil }.map { "\($0.id):\($0.waitingOn?.id ?? $0.pendingAskID ?? "")" }
        sessionCount = sessions.count
        usageSessions = sessions.filter { $0.archived != true && ($0.isWorking || $0.current) }
        hostGroups = Dictionary(ordered.compactMap { host in host.snapshot.map { (host.id, HostGroups(channels: $0.channels, relays: $0.relays)) } }, uniquingKeysWith: { first, _ in first })
        sessionProjects = Dictionary(sessions.compactMap { row in row.project.map { (row.id, $0.id) } }, uniquingKeysWith: { first, _ in first })
        projectSessionIDs = inDaemonOrder(sessions).reduce(into: [:]) { ids, row in
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

    func dismissDeliveryProblem(_ problem: DeliveryProblem) async throws {
        guard let hostID = problem.hostID, let connection = connections[hostID], connection.shownReachable else {
            throw AgenthailAPIError.unavailable("Connect to your computer first.")
        }
        let key = "delivery:\(problem.id)"
        guard !pendingControls.contains(key) else { return }
        pendingControls.insert(key)
        defer { pendingControls.remove(key) }
        try await connection.api.action("delivery-dismiss", deliveryID: problem.deliveryId)
        connection.updateSnapshot { $0.deliveryProblems = $0.deliveryProblems?.filter { $0.deliveryId != problem.deliveryId } }
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

    private func deliveryLabel(_ receipt: DeliveryReceipt?) -> String {
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

    private func inboxHosts() throws -> [InboxPage.Host] {
        let direct = hostOrder.compactMap { connections[$0] }.filter(\.shownReachable).map { InboxPage.Host(id: $0.id, name: $0.name, api: $0.api) }
        let remote = catalog.hosts.compactMap { host -> InboxPage.Host? in
            guard let target = catalog.target(forHost: host.id), let peer = target.remote, let connection = connections[target.hostID] else { return nil }
            return InboxPage.Host(id: host.id, name: host.name, api: connection.api, remote: peer)
        }
        let hosts = direct + remote
        guard !hosts.isEmpty else { throw AgenthailAPIError.unavailable("Connect to your computer first.") }
        return hosts
    }

    func inboxPage(before cursors: [String: Int64]? = nil, partial: (@MainActor (InboxPage) -> Void)? = nil) async throws -> InboxPage {
        try await InboxPage.load(from: inboxHosts(), before: cursors, partial: partial)
    }

    func loadDecisions() async -> DecisionInbox {
        guard let hosts = try? inboxHosts() else { return DecisionInbox() }
        var inbox = await DecisionInbox.load(from: hosts)
        let older = hostOrder.compactMap { connections[$0] }.filter { $0.snapshot != nil && $0.snapshot?.decisionsRev == nil }.map(\.name)
        inbox.outdated = Array(Set(inbox.outdated + older)).sorted()
        return inbox
    }

    func answerDecision(_ request: SessionRequest, sessionID: String, decision: RequestDecision) async -> DecisionAnswer {
        guard answeringRequest == nil else { return .busy }
        answeringRequest = request.id
        defer { answeringRequest = nil }
        var failure: RequestAnswerFailure?
        do {
            let (api, route) = try resolve(sessionID)
            _ = try await api.respond(sessionID: route.sessionID, requestID: request.id, decision: decision)
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
    func updateQueue(_ item: QueueState, retry: Bool) async throws -> Bool {
        try await updateQueue(id: item.id, hostID: item.hostID, retry: retry)
    }

    @discardableResult
    func updateQueue(id: Int64, hostID: String?, retry: Bool) async throws -> Bool {
        guard let hostID else { throw AgenthailAPIError.unavailable("Connect to your computer first.") }
        let (api, target) = try reach(hostID)
        let key = "queue:\(hostID)/\(id)"
        guard !pendingControls.contains(key) else { return false }
        pendingControls.insert(key)
        defer { pendingControls.remove(key) }
        try await api.action(retry ? "queue-retry" : "queue-cancel", queueID: id, host: target.remote)
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
        returnToDraft(cancelled.message ?? item.message, attachments: cancelled.attachments ?? [], id: draftKey(for: item))
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
            returnToDraft(message, attachments: attachments, id: id)
            await connections[route.hostID]?.refresh(fresh: true)
            throw AgenthailAPIError.unavailable("Steer may not have reached the agent. The message is back in the composer; check the conversation before resending. \(AgenthailAPIError.userMessage(error))")
        }
        await connections[route.hostID]?.refresh(fresh: true)
        await refreshSession(id)
        return true
    }

    private func returnToDraft(_ message: String, attachments: [QueueAttachment], id: String) {
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

    func canFork(_ session: SessionState) -> Bool { session.surface == "codex" && !session.isReadOnly }

    /// Forks a Codex session from just before a prompt and opens the fork with
    /// that prompt in its composer.
    func fork(_ session: SessionState, before prompt: PromptItem) async throws {
        guard canFork(session), let turnID = prompt.turnId else { return }
        let (connection, route) = try resolveServer(session.id)
        let response: ForkResponse = try await connection.api.postAction(ForkRequest(sessionId: route.sessionID, fork: ForkRequest.Point(beforeTurnId: turnID)), idempotencyKey: UUID().uuidString)
        guard let forked = response.result?.session.id else { throw AgenthailAPIError.invalidResponse }
        let id = HostCatalog.qualify(HostCatalog.owner(of: route.sessionID) ?? route.hostID, forked)
        drafts[id] = prompt.text
        await refresh(host: route.hostID)
        openSession(id)
    }

    private struct ForkRequest: Encodable {
        struct Point: Encodable { let beforeTurnId: String }
        var action = "session-fork"
        let sessionId: String
        let fork: Point
    }

    private struct ForkResponse: Decodable {
        struct Result: Decodable {
            struct Session: Decodable { let id: String }
            let session: Session
        }
        let result: Result?
    }

    private func draftKey(for item: QueueState) -> String {
        let known = Set((snapshot?.sessions ?? []).map(\.id))
        let qualified = item.hostID.map { HostCatalog.qualify($0, item.sessionId) }
        return rekeys.resolve(qualified.flatMap { known.contains($0) ? $0 : nil } ?? item.sessionId)
    }

    func addProject(path: String, title: String?, host: String?) async -> ProjectChange {
        await projectChange(refreshing: host) {
            let (api, target) = try self.reach(host)
            let (project, created) = try await api.addProject(path: path, title: title, host: target.remote)
            let name = project?.displayName ?? "This folder"
            return created ? "Added project \(name)." : "\(name) is already a project."
        }
    }

    func addFolder(of session: SessionState) async -> ProjectChange {
        await projectChange(refreshing: nil) {
            let (connection, _) = try self.resolveServer(session.id)
            let owner = HostCatalog.owner(of: session.id)
            let (project, created) = try await connection.api.addProject(path: session.cwd ?? "", host: owner == connection.id ? nil : owner)
            let name = project?.displayName ?? "This folder"
            return created ? "Added project \(name)." : "\(name) is already a project."
        }
    }

    func makeLead(_ sessionID: String, replace: Bool) async -> ProjectChange {
        await projectChange(refreshing: nil) {
            let (api, route) = try self.resolve(sessionID)
            let lead = try await api.makeLead(sessionID: route.sessionID, replace: replace)
            return "@\(lead.name) leads \(lead.projectRoot)."
        }
    }

    func renameProject(_ project: ListedProject, to title: String) async -> ProjectChange {
        let owners = project.state.checkouts.filter { $0.added == true }.map { ProjectRouting.owner($0.host, of: project) }
        return await projectChange(refreshing: nil) {
            try await self.eachOwner(of: owners, in: project, done: "Renamed to \(title).") { api, projectID in
                try await api.renameProject(projectID, title: title)
            }
        }
    }

    func removeProject(_ projectID: String, of project: ListedProject) async -> ProjectChange {
        let owner = HostCatalog.owner(of: projectID) ?? project.hostID
        return await projectChange(refreshing: nil) {
            let (api, target) = try self.reach(owner)
            try await api.projectAction("project-remove", projectID: target.remote == nil ? ProjectRouting.local(projectID, of: owner) : projectID)
            return "Project removed."
        }
    }

    func clearLead(of project: ListedProject) async -> ProjectChange {
        let owners = project.state.leads.map { ProjectRouting.owner($0.host, of: project) }
        return await projectChange(refreshing: nil) {
            try await self.eachOwner(of: owners, in: project, done: "\(project.state.displayName) has no lead now.") { api, projectID in
                try await api.clearLead(projectID: projectID)
            }
        }
    }

    func startLead(of project: ListedProject, in checkout: ProjectState.Checkout, surface: String, name: String, replace: Bool) async -> ProjectChange {
        await projectChange(refreshing: nil) {
            let (api, target) = try self.reach(ProjectRouting.owner(checkout.host, of: project))
            let lead = try await api.startLead(surface: surface, folder: checkout.rootPath, host: target.remote, name: name, replace: replace)
            return "@\(lead.name) leads \(project.state.displayName); its first message starts it."
        }
    }

    private func eachOwner(of owners: [String], in project: ListedProject, done: String, _ action: (AgenthailAPI, String) async throws -> Void) async throws -> String {
        var routes: [(owner: String, target: HostTarget)] = []
        var unreached: [String] = []
        for owner in owners.isEmpty ? [project.hostID] : owners where !routes.contains(where: { $0.owner == owner }) && !unreached.contains(owner) {
            guard let target = catalog.target(forHost: owner) else { unreached.append(owner); continue }
            routes.append((owner, target))
        }
        func send(_ route: (owner: String, target: HostTarget)) async throws {
            guard let api = connections[route.target.hostID]?.api else { throw AgenthailAPIError.unavailable("\(hostName(route.owner)) can't be reached.") }
            try await action(api, route.target.remote.map { $0 + HostCatalog.separator + project.state.id } ?? project.state.id)
        }
        if unreached.isEmpty, routes.count == 1 {
            try await send(routes[0])
            return done
        }
        var failures = unreached.map { "\(hostName($0)) can't be reached" }
        var succeeded = false
        for route in routes {
            do {
                try await send(route)
                succeeded = true
            } catch {
                failures.append("\(hostName(route.owner)): \(AgenthailAPIError.userMessage(error))")
            }
        }
        guard !failures.isEmpty else { return done }
        throw AgenthailAPIError.unavailable((succeeded ? done + " Not changed on " : "Not changed on ") + failures.joined(separator: "; ") + ".")
    }

    private func hostName(_ hostID: String) -> String {
        connections[hostID]?.name ?? catalog.hosts.first { $0.id == hostID }?.name ?? hostID
    }

    func listed(_ state: ProjectState) -> ListedProject? {
        projects.values.first { $0.state == state }
    }

    func canAddFolder(_ session: SessionState) -> Bool {
        ProjectMenu.canAddFolder(session) && routing.route(for: session.id) != nil
    }

    func canMakeLead(_ session: SessionState) -> Bool {
        guard ProjectMenu.canMakeLead(session, projects: []), routing.route(for: session.id) != nil else { return false }
        return !projects.values.contains { listed in
            listed.state.leads.contains { lead in
                guard let sessionID = lead.sessionId else { return false }
                return HostCatalog.qualify(ProjectRouting.owner(lead.host, of: listed), sessionID) == session.id
            }
        }
    }

    func settle(_ change: ProjectChange) {
        switch change {
        case .done: operationError = nil
        case .confirmReplace(let message), .failed(let message): operationError = message
        }
    }

    func creationHost(for checkout: ProjectState.Checkout, of project: ListedProject) -> String {
        ProjectRouting.owner(checkout.host, of: project)
    }

    private func projectChange(refreshing hostID: String?, _ body: () async throws -> String) async -> ProjectChange {
        let change = await ProjectChange.run(body)
        if let hostID, connections[hostID] != nil { await refresh(host: hostID) } else { _ = await refresh(fresh: true) }
        return change
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
            let (api, route) = try resolve(sessionID)
            try await api.action("session-lifecycle-resume", sessionID: route.sessionID)
            operationError = nil
        } catch {
            operationError = error.localizedDescription
        }
    }

    /// Stops a session and resumes it, which clears a prompt nothing shows.
    func stopAndResume(_ sessionID: String) {
        guard case let (api, route)? = try? resolve(sessionID) else { return }
        Task {
            do {
                try await api.action("session-lifecycle-stop", sessionID: route.sessionID)
                try await api.action("session-lifecycle-resume", sessionID: route.sessionID)
                operationError = nil
            } catch {
                operationError = error.localizedDescription
            }
            await refreshSession(sessionID)
        }
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
                repairingPush.insert(hostID)
                await cancelPushSetup(hostID)?.value
                if let previous {
                    if let relayURL = previous.pushRelayURL, let stored = storedPush(hostID),
                       let failure = await revokeBeforeRepair(PushRelayClient(baseURL: relayURL, session: networkSession), stored.registration) {
                        try? await pairedAPI.revokeCurrentDevice()
                        throw AgenthailAPIError.unavailable(failure)
                    }
                    if !previous.isPreview {
                        try? await previous.api.removePush()
                        try? await previous.api.revokeCurrentDevice()
                    }
                    previous.stop()
                    clearStoredPush(hostID)
                    store.prune { $0.hasPrefix(hostID + HostCatalog.separator) }
                }
                let credential = PairedHostCredential(hostID: hostID, hostName: response.host.name, endpoint: pendingPairing.endpoint, token: response.token, pairedAt: previous == nil ? Date() : PairedCredential.all().first { $0.hostID == hostID }?.pairedAt ?? Date())
                try PairedCredential.save(credential)
                add(HostConnection(credential: credential, session: networkSession, drops: connectionDrops))
                repairingPush.remove(hostID)
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
                if repairingPush.remove(pendingPairing.hostID) != nil, let pushDeviceToken,
                   let previous = connections[pendingPairing.hostID], previous.reachable {
                    registerPush(previous, deviceToken: pushDeviceToken)
                }
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
        retryComposerStore()
        guard isPaired else {
            refreshNotificationRegistration()
            return
        }
        showNotificationAuthorization()
        connect()
        sendQueue.wake()
    }

    @discardableResult
    func refresh(fresh: Bool = false) async -> Bool {
        await HostRefresh.all(hostOrder.compactMap { connections[$0] }, fresh: fresh, within: hostRefreshWait)
    }

    private func refresh(host hostID: String) async {
        _ = await connections[hostID]?.refresh(fresh: true)
    }

    private var listedHosts: [HostConnection] { hostOrder.compactMap { connections[$0] } }

    func listMore(_ filter: SessionFilter) async {
        await SessionScopes.readMore(SessionScopes.targets(filter, on: listedHosts))
    }

    func canListMore(_ filter: SessionFilter) -> Bool { listing.more.contains(filter) }

    private func earlierTargets(_ project: ListedProject) -> [ScopeTarget] {
        let request = ScopeRequest(scope: "all", projectID: project.state.id)
        let shared = project.state.id.hasPrefix(HostCatalog.repositoryPrefix)
        let direct = shared ? project.servers + project.state.checkouts.map { ProjectRouting.owner($0.host, of: project) } : []
        var seen: Set<String> = []
        return ([project.hostID] + direct).compactMap { hostID in
            guard seen.insert(hostID).inserted, let host = connections[hostID] else { return nil }
            return ScopeTarget(host: host, request: request)
        }
    }

    func listEarlier(_ project: ListedProject) async { await SessionScopes.readMore(earlierTargets(project)) }

    func hasEarlier(_ project: ListedProject) -> Bool { listing.earlier.contains(ProjectContents.key(of: project)) }

    var listingError: String? { listing.error }

    func projectSessions(_ project: ListedProject) -> [SessionState] {
        let ids = projectSessionIDs[ProjectContents.key(of: project)] ?? []
        let lead = project.state.leads.first.flatMap { ProjectContents.leadSessionID($0, of: project) }
        return (lead.map { ids.contains($0) ? ids : ids + [$0] } ?? ids).compactMap(session)
    }

    func searchScope(_ query: String, filter: SessionFilter) async {
        await SessionScopes.search(query.trimmingCharacters(in: .whitespacesAndNewlines), filter: filter, on: listedHosts)
    }

    func mentionSessions(for sessionID: String, query: String) async -> [SessionState]? {
        guard let host = server(for: sessionID), let rows = try? await host.mentions(query) else { return nil }
        return rows.map { HostCatalog.merge($0, servedBy: host.id, ownerName: { id in self.catalog.hosts.first { $0.id == id }?.name }, reachable: true).row }
    }

    struct ListKey: Equatable {
        let scope: SessionFilter
        let search: String
    }

    // Families hold session cells, so a change inside one session leaves the tree equal.
    func sessionTree(scope: SessionFilter, search: String) -> SessionTree {
        _ = listRevision
        let key = ListKey(scope: scope, search: search)
        if let listed = listedTree, listed.key == key { return listed.tree }
        let tree = buildTree(key)
        listedTree = (key, tree)
        return tree
    }

    private func rederiveTree() {
        let key = listedTree?.key ?? ListKey(scope: .recent, search: "")
        let tree = buildTree(key)
        if let listed = listedTree, listed.tree == tree { return }
        listedTree = (key, tree)
        listRevision &+= 1
    }

    private func buildTree(_ key: ListKey) -> SessionTree {
        if orderHold.held, heldKey != key { orderHold.release() }
        let rows = Dictionary((snapshot?.sessions ?? []).map { ($0.id, $0) }, uniquingKeysWith: { first, _ in first })
        let shown = orderHold.shown(list: listOrder(scope: key.scope, search: key.search), current: currentOrder, rows: rows)
        heldList = shown.list
        return SessionTree.ordered(list: shown.list, current: shown.current, rows: shown.rows, attention: sessionAttention, cells: treeCells(shown.rows))
    }

    private func treeCells(_ rows: [String: SessionState]) -> [String: SessionCell] {
        var all = cells.byID
        guard orderHold.held else {
            heldCells = [:]
            return all
        }
        for (id, row) in rows where all[id] == nil {
            let cell = heldCells[id] ?? SessionCell(row)
            heldCells[id] = cell
            all[id] = cell
        }
        return all
    }

    private var orderHosts: [HostConnection] { listedHosts.filter(\.shownReachable) + listedHosts.filter { !$0.shownReachable } }

    private func joinedOrder(_ pick: (HostConnection) -> ScopePage.View?) -> ScopePage.View? {
        ScopePage.View.joined(orderHosts.compactMap { host in pick(host)?.renamed { HostCatalog.qualify(host.id, $0) } })
    }

    var currentOrder: ScopePage.View? { joinedOrder(\.currentView) }

    /// Sessions in the daemon's order: Current first, then each loaded listing; others keep their place after.
    private func inDaemonOrder(_ sessions: [SessionState]) -> [SessionState] {
        var order: [String: Int] = [:]
        let listings = listedHosts.flatMap { host in host.reader.requests.sorted().compactMap { host.view($0)?.renamed { HostCatalog.qualify(host.id, $0) } } }
        for member in ([currentOrder].compactMap { $0 } + listings).flatMap({ $0.families.flatMap(\.members) }) where order[member.id] == nil {
            order[member.id] = order.count
        }
        return sessions.enumerated().sorted { lhs, rhs in
            let (left, right) = (order[lhs.element.id] ?? Int.max, order[rhs.element.id] ?? Int.max)
            return left != right ? left < right : lhs.offset < rhs.offset
        }.map(\.element)
    }

    private var orderSignature: [ScopePage.View?] {
        listedHosts.flatMap { host in [host.currentView, host.reader.foundView] + host.reader.requests.sorted().map(host.view) }
    }

    func searchPending(_ search: String) -> Bool { listing.searchPending(search) }

    func listOrder(scope: SessionFilter, search: String) -> ScopePage.View? {
        if !search.isEmpty { return joinedOrder { $0.reader.foundView } }
        if scope == .recent { return currentOrder }
        return joinedOrder { $0.view(ScopeRequest(scope: scope.viewScope)) }
    }

    /// Keeps the list's order while a finger is on it; rows still update in place.
    func holdOrder(_ holding: Bool) {
        guard holding != orderHold.held else { return }
        if holding {
            let rows = Dictionary((snapshot?.sessions ?? []).map { ($0.id, $0) }, uniquingKeysWith: { first, _ in first })
            orderHold.hold(list: heldList, current: currentOrder, rows: rows)
            heldKey = listedTree?.key
        } else {
            orderHold.release()
            rederiveTree()
        }
    }

    func loadSession(_ id: String) async {
        let id = rekeys.resolve(id)
        let switching = selectedSessionID != id
        if let previous = selectedSessionID, previous != id {
            drafts[previous] = composer
            attachmentDrafts[previous] = composerAttachments
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
            composer = drafts[id] ?? ""
            composerAttachments = attachmentDrafts[id] ?? []
            attachmentProblem = nil
            announceLostAttachments(for: id)
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

    func syncRequests(for session: SessionState) async {
        if retiredAsks.settles(session) { retiredAsks.settle(session) }
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

    func openSession(_ sessionID: String) {
        requestedSessionID = rekeys.resolve(sessionID)
    }

    private func followRekeys(_ retired: [String: String]) {
        rekeys.record(retired)
        guard !rekeys.isEmpty else { return }
        _ = rekeys.carry(&drafts)
        _ = rekeys.carry(&attachmentDrafts)
        store.carry(rekeys)
        var held = stashes
        if rekeys.carry(&held) { stashes = held }
        sendQueue.carry(rekeys)
        var failures = failedSends
        if rekeys.carry(&failures) { failedSends = failures }
        var refused = refusedSends
        if rekeys.carry(&refused) { refusedSends = refused }
        _ = rekeys.carry(&lostAttachments)
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

    func pickHolder(_ holder: SessionHolder?, for sessionID: String) {
        HolderPins.shared.pick(holder.map(HolderPin.init), for: sessionID)
        let picks = HolderPins.shared.all
        if holderPicks != picks { holderPicks = picks }
    }

    func send(to session: SessionState) {
        let message = composer.trimmingCharacters(in: .whitespacesAndNewlines)
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
                let stored = try await api.upload(send.attachments, sessionID: route.sessionID)
                sendQueue.setUploaded(stored, for: send)
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
        refusedSends[send.sessionID] = RefusedSend(message: send.message, attachments: send.attachments, deliveryID: kept?.deliveryID, deletable: kept?.deletable ?? false)
        if selectedSessionID != send.sessionID { operationError = reason }
        returnToComposer(send)
        saveComposerState()
    }

    func deleteRefusedSend(in sessionID: String) {
        guard let refused = refusedSends[sessionID], refused.canDelete else { return }
        guard let deliveryID = refused.deliveryID else {
            dropRefusedSend(refused, in: sessionID)
            return
        }
        Task {
            do {
                try await resolve(sessionID).api.action("delivery-delete", deliveryID: deliveryID)
                dropRefusedSend(refused, in: rekeys.resolve(sessionID))
            } catch {
                operationError = AgenthailAPIError.userMessage(error)
            }
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

    private func retryComposerStore() {
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
        for (session, uploads) in saved.uploadedAttachments ?? [:] {
            let restored = uploads.map(ComposerAttachment.init(uploaded:))
            if session == selectedSessionID {
                composerAttachments = ComposerAttachments.adding(restored, to: composerAttachments).attachments
            } else {
                attachmentDrafts[session] = ComposerAttachments.adding(restored, to: attachmentDrafts[session] ?? []).attachments
            }
        }
        for (session, uploads) in saved.stashedUploads ?? [:] {
            let restored = uploads.map(ComposerAttachment.init(uploaded:))
            if var stash = stashes[session] {
                stash.attachments = ComposerAttachments.adding(restored, to: stash.attachments).attachments
                stashes[session] = stash
            } else {
                attachmentDrafts[session] = ComposerAttachments.adding(restored, to: attachmentDrafts[session] ?? []).attachments
            }
        }
        lostAttachments.merge(saved.attachmentNames) { $0 + $1 }
        sendQueue.restore(saved.sends.compactMap(\.resumable))
        for record in saved.sends where record.resumable == nil {
            addToDraft(record.message, for: record.sessionID)
            failedSends[record.sessionID] = "Not sent: the app closed before its attachment uploaded. Add it again."
        }
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
        var saved = ComposerStore.Saved(drafts: drafts, stashes: stashes.mapValues(\.text), sends: sendQueue.pending.values.flatMap { $0 }.map(ComposerStore.Saved.Send.init))
        if let selectedSessionID { saved.drafts[selectedSessionID] = composer }
        saved.drafts = saved.drafts.filter { !$0.value.isEmpty }
        saved.attachmentNames = attachmentNamesToSave()
        var uploaded = attachmentDrafts.filter { $0.key != selectedSessionID }
        if let selectedSessionID { uploaded[selectedSessionID] = composerAttachments }
        saved.uploadedAttachments = uploaded.mapValues { $0.compactMap(\.uploaded) }.filter { !$0.value.isEmpty }
        saved.stashedUploads = stashes.mapValues { $0.attachments.compactMap(\.uploaded) }.filter { !$0.value.isEmpty }
        composerStore.save(saved)
    }

    private func attachmentNamesToSave() -> [String: [String]] {
        var names = lostAttachments
        func add(_ attachments: [ComposerAttachment], for session: String) {
            let lost = attachments.filter { $0.uploaded == nil }
            if !lost.isEmpty { names[session, default: []].append(contentsOf: lost.map(\.name)) }
        }
        for (session, attachments) in attachmentDrafts where session != selectedSessionID { add(attachments, for: session) }
        for (session, stash) in stashes { add(stash.attachments, for: session) }
        if let selectedSessionID { add(composerAttachments, for: selectedSessionID) }
        return names
    }

    private func announceLostAttachments(for sessionID: String) {
        guard let names = lostAttachments.removeValue(forKey: sessionID) else { return }
        attachmentProblem = "Not kept when the app closed: \(names.joined(separator: ", ")). Add again to send."
        saveComposerStateSoon()
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

    func setProjectArchived(_ projectID: String, _ archived: Bool) async {
        let action = archived ? "project-archive" : "project-unarchive"
        do {
            guard let owner = HostCatalog.owner(of: projectID) else {
                try await setRepositoryArchived(projectID, action: action)
                operationError = nil
                return
            }
            let (api, target) = try reach(owner)
            let id = target.remote == nil ? String(projectID.dropFirst(owner.count + HostCatalog.separator.count)) : projectID
            try await api.projectAction(action, projectID: id)
            operationError = nil
            await refresh(host: target.hostID)
        } catch {
            operationError = error.localizedDescription
        }
    }

    // A repository project has one id on every computer; each computer that
    // serves one of its sessions archives its own checkout.
    private func setRepositoryArchived(_ projectID: String, action: String) async throws {
        let servers = Set((snapshot?.sessions ?? []).filter { $0.project?.id == projectID }.compactMap { catalog.server(of: $0.id) })
        let reachable = hostOrder.filter { servers.contains($0) && connections[$0]?.shownReachable == true }
        guard !reachable.isEmpty else { throw AgenthailAPIError.unavailable("Connect to your computer first.") }
        var failures: [String] = []
        for hostID in reachable {
            guard let connection = connections[hostID] else { continue }
            do {
                try await connection.api.projectAction(action, projectID: projectID)
            } catch {
                failures.append("\(connection.name): \(AgenthailAPIError.userMessage(error))")
            }
            await refresh(host: hostID)
        }
        if !failures.isEmpty { throw AgenthailAPIError.unavailable(failures.joined(separator: "\n")) }
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
        cancelPushSetup(hostID)
        pushFailures.removeValue(forKey: hostID)
        connections.removeValue(forKey: hostID)
        let wasAppearanceHost = hostOrder.first == hostID
        hostOrder.removeAll { $0 == hostID }
        if wasAppearanceHost, let next = hostOrder.first.flatMap({ connections[$0] }), next.reachable { appearance.load(from: next.api) }
        KeychainStore.remove(PairedCredential.account(for: hostID))
        KeychainStore.remove(HostConnection.relayAccount(hostID))
        PushReceipts.forget(host: hostID)
        connectionDrops?.forget(host: hostID)
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
        sendQueue.removeAll(where: owned)
        failedSends = failedSends.filter { !owned($0.key) }
        refusedSends = refusedSends.filter { !owned($0.key) }
        lostAttachments = lostAttachments.filter { !owned($0.key) }
        store.prune(removing: owned)
        stashes = stashes.filter { !owned($0.key) }
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
        pushTurnedOff = false
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
        pushTurnedOff = true
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
        deviceRegistrationFailure = nil
        for id in hostOrder {
            if let connection = connections[id], connection.reachable { registerPush(connection, deviceToken: deviceToken) }
        }
        updateNotificationStatus()
    }

    func pushRegistrationFailed(_ reason: String) {
        deviceRegistrationFailure = reason
        updateNotificationStatus()
    }

    func setAppActive(_ active: Bool) {
        appActive = active
        if !active {
            for id in Array(pushRetries.keys) { cancelPushSetup(id) }
            return
        }
        guard let pushDeviceToken else { return }
        for id in hostOrder where pushStatus[id] != "Enabled" {
            if let connection = connections[id], connection.reachable { registerPush(connection, deviceToken: pushDeviceToken) }
        }
    }

    private func revokeBeforeRepair(_ relay: PushRelayClient, _ registration: PushRegistration) async -> String? {
        var failure: Error
        do { try await relay.revoke(registration); return nil } catch { failure = error }
        if case let .transient(retryAfter) = PushSetupFailure.classify(failure),
           let delay = PushSetupFailure.delay(afterFailure: 1, retryAfter: retryAfter) {
            try? await Task.sleep(for: .seconds(delay))
            do { try await relay.revoke(registration); return nil } catch { failure = error }
        }
        if case .transient = PushSetupFailure.classify(failure) {
            return "Couldn't reach the notification relay to remove this iPhone's earlier registration, so the computer was not paired again. Check the connection and pair again."
        }
        return "The notification relay would not remove this iPhone's earlier registration, so the computer was not paired again: \(failure.localizedDescription)"
    }

    @discardableResult
    private func cancelPushSetup(_ hostID: String) -> Task<Void, Never>? {
        guard let retry = pushRetries.removeValue(forKey: hostID) else { return nil }
        retry.task.cancel()
        if pushStatus[hostID] == "Enabling" { pushStatus[hostID] = "Reconnect to finish setup" }
        updateNotificationStatus()
        return retry.task
    }

    private func registerPush(_ connection: HostConnection, deviceToken: String) {
        let hostID = connection.id
        guard !connection.isPreview, appActive, !pushTurnedOff, !repairingPush.contains(hostID) else { return }
        if let running = pushRetries[hostID] {
            if running.connection == ObjectIdentifier(connection) { return }
            cancelPushSetup(hostID)
        }
        guard let relayURL = connection.pushRelayURL else {
            pushStatus[hostID] = "Reconnect to finish setup"
            updateNotificationStatus()
            return
        }
        pushStatus[hostID] = "Enabling"
        pushFailures[hostID] = nil
        updateNotificationStatus()
        let id = UUID()
        let task = Task {
            defer {
                if pushRetries[hostID]?.id == id { pushRetries[hostID] = nil }
                updateNotificationStatus()
            }
            var failures = 0
            while true {
                do {
                    try await setUpPush(connection, relayURL: relayURL, deviceToken: deviceToken)
                    return
                } catch {
                    guard !Task.isCancelled, connections[hostID] === connection else { return }
                    failures += 1
                    if case let .transient(retryAfter) = PushSetupFailure.classify(error),
                       let delay = PushSetupFailure.delay(afterFailure: failures, retryAfter: retryAfter) {
                        try? await Task.sleep(for: .seconds(delay))
                        guard !Task.isCancelled, appActive, connection.reachable, connections[hostID] === connection else { return }
                        continue
                    }
                    pushStatus[hostID] = "Setup failed"
                    pushFailures[hostID] = error.localizedDescription
                    return
                }
            }
        }
        pushRetries[hostID] = (id, ObjectIdentifier(connection), task)
    }

    private func setUpPush(_ connection: HostConnection, relayURL: URL, deviceToken: String) async throws {
        let hostID = connection.id
        let relay = PushRelayClient(baseURL: relayURL, session: networkSession)
        let endpoint = connection.credential.endpoint.absoluteString
        if let stored = storedPush(hostID), !stored.registration.needsRenewal, stored.deviceToken == deviceToken,
           stored.environment == PushRelayClient.environment, stored.endpoint == endpoint {
            do {
                try await relay.validate(stored.registration)
                try Task.checkCancellation()
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
        guard !Task.isCancelled, connections[hostID] === connection else {
            try? await relay.revoke(registration)
            clearStoredPush(hostID)
            throw CancellationError()
        }
        try await connection.api.configurePush(installationID: registration.installationId, credential: registration.credential)
        pushStatus[hostID] = "Enabled"
    }

    private func updateNotificationStatus() {
        notificationProblems = (deviceRegistrationFailure.map { [$0] } ?? [])
            + hostOrder.compactMap { id in pushFailures[id].map { "\(connections[id]?.name ?? id): \($0)" } }
        let statuses = hostOrder.filter { !(connections[$0]?.isPreview ?? true) }.map { pushStatus[$0] ?? "Not enabled" }
        if deviceRegistrationFailure != nil { notificationStatus = "Setup failed" }
        if statuses.isEmpty { return }
        if deviceRegistrationFailure != nil || statuses.contains("Setup failed") { notificationStatus = "Setup failed" }
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
            case "goal":
                sessionMetadata.stream(goal: event.data.goal)
                detail.goal = event.data.goal
                changed = true
            case "source-error" where event.data.op == "reset":
                error = event.data.reason ?? "The session source restarted."
                streamOutranPage = true
            default:
                guard var timeline = detail.timeline, !event.data.itemId.isEmpty else { continue }
                if event.data.op == "remove" {
                    timeline.items.removeAll { $0.id == event.data.itemId }
                    older.removeAll { $0.id == event.data.itemId }
                } else {
                    SessionStore.place(Self.timelineItem(event), in: &timeline, older: &older)
                }
                detail.timeline = timeline
                error = nil
                changed = true
            }
        }
        if older != olderActivity { olderActivity = older }
        if changed { selectedDetail = detail }
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
        if !host.reachable { cancelPushSetup(host.id) }
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
        if let pushDeviceToken { registerPush(host, deviceToken: pushDeviceToken) }
        else { refreshNotificationRegistration() }
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

    private static func pushAccount(_ hostID: String) -> String { "push:\(hostID)" }

    private func storedPush(_ hostID: String) -> StoredPush? {
        guard let value = KeychainStore.get(Self.pushAccount(hostID)) else { return nil }
        return try? JSONDecoder().decode(StoredPush.self, from: Data(value.utf8))
    }

    private func disableNotifications(status: String) async {
        for id in Array(pushRetries.keys) { await cancelPushSetup(id)?.value }
        deviceRegistrationFailure = nil
        for id in hostOrder {
            guard let connection = connections[id], !connection.isPreview else { continue }
            pushFailures[id] = nil
            do {
                try await connection.api.removePush()
                if let relayURL = connection.pushRelayURL, let stored = storedPush(id) {
                    try? await PushRelayClient(baseURL: relayURL, session: networkSession).revoke(stored.registration)
                }
                clearStoredPush(id)
            } catch {
                pushFailures[id] = "Could not turn off delivery. Reconnect and try again. \(error.localizedDescription)"
            }
            pushStatus[id] = status
        }
        updateNotificationStatus()
        notificationStatus = status
    }

    private func clearStoredPush(_ hostID: String) {
        KeychainStore.remove(Self.pushAccount(hostID))
    }
}

final class ComposerDraft: ObservableObject {
    @Published var text = ""
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
