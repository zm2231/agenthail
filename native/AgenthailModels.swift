import Foundation

struct ClaudeCreationSettings {
    var name = ""
    var worktree = ""
    var agent = ""
    var effort = ""
    var permissionMode = ""
    var fields: [String: String] {
        ["name": name, "worktree": worktree, "agent": agent, "effort": effort, "permissionMode": permissionMode].filter { !$0.value.isEmpty }
    }
}

struct SessionCreationOptions: Decodable {
    struct Surface: Decodable, Identifiable { let id: String; let workspace: Bool; var connected: Bool? = nil }
    var host: SessionHost? = nil
    let surfaces: [Surface]
    let workspaces: [String]
    var launchers: [LauncherOption]? = nil
}

struct CreationHost: Hashable, Identifiable {
    let id: String
    let name: String
    let state: String
    var isLocal: Bool { id.isEmpty }
    var isAvailable: Bool { state == "connected" }
    var stateLabel: String { HostHealth(id: id, name: name, state: state).stateLabel }
}

/// Where a new session starts and the launch catalog read from that host.
/// An empty host id is this machine. Loads carry the generation they were
/// started for, so an answer for a host the user already left is dropped.
struct CreationHostPicker {
    private(set) var hosts: [CreationHost] = []
    private(set) var selected = ""
    private(set) var options: SessionCreationOptions?
    private(set) var models: [String: [ModelOption]] = [:]
    private(set) var loadError: String?
    private(set) var generation = 0

    init(daemon: DaemonState?, selectedSession: SessionState?) {
        update(daemon: daemon)
        if let host = selectedSession?.host, host.state != "local", hosts.contains(where: { $0.id == host.id && $0.isAvailable }) {
            selected = host.id
        }
    }

    var isVisible: Bool { hosts.count > 1 }
    var selectedHost: CreationHost? { hosts.first { $0.id == selected } }
    var hostQuery: String? { selected.isEmpty ? nil : selected }
    var isLoading: Bool { options == nil && loadError == nil }
    var canStart: Bool { options != nil && (selected.isEmpty || selectedHost?.isAvailable == true) }
    var folders: [String] { options?.workspaces ?? [] }

    mutating func update(daemon: DaemonState?) {
        let paired = daemon?.hosts ?? []
        if let local = daemon?.host, !paired.isEmpty {
            hosts = [CreationHost(id: "", name: local.name, state: "connected")] + paired.map { CreationHost(id: $0.id, name: $0.name, state: $0.state) }
        } else {
            hosts = []
        }
        if !selected.isEmpty && selectedHost == nil {
            selected = ""
            options = nil
            models = [:]
            loadError = nil
            generation += 1
        }
    }

    @discardableResult
    mutating func select(_ id: String) -> Bool {
        guard id != selected, hosts.contains(where: { $0.id == id && $0.isAvailable }) else { return false }
        selected = id
        options = nil
        models = [:]
        loadError = nil
        generation += 1
        return true
    }

    func owns(_ session: SessionState?) -> Bool {
        guard let session else { return false }
        return (session.remoteHostName == nil ? "" : session.host?.id ?? "") == selected
    }

    mutating func receive(options next: SessionCreationOptions, for loaded: Int) {
        guard loaded == generation else { return }
        options = next
        loadError = nil
    }

    mutating func receive(models next: [ModelOption], agent: String, for loaded: Int) {
        guard loaded == generation else { return }
        models[agent] = next
    }

    mutating func fail(_ message: String, for loaded: Int) {
        guard loaded == generation else { return }
        loadError = message
    }
}
struct CreationModels: Decodable { let models: [ModelOption] }
struct QueueResponse: Decodable { let items: [QueueState] }
struct SessionCreationReceipt: Decodable {
    let ok: Bool
    let unknown: Bool?
    let status: String?
    let accepted: Bool?
    let retryable: Bool?
    let warning: String?
    let session: RawSession?
    let sessionId: String?
    let error: String?
    let launcher: String?
    let location: SessionRuntime.Location?
    var id: String? { session?.id ?? sessionId }
}

struct APIVersion: Decodable {
    let protocolVersion: Int
    let minimumProtocol: Int
    let maximumProtocol: Int
    let pushRelayUrl: String?

    enum CodingKeys: String, CodingKey {
        case protocolVersion = "protocol"
        case minimumProtocol
        case maximumProtocol
        case pushRelayUrl
    }
}

struct Capabilities: Codable, Hashable {
    var send = false
    var stream = false
    var reply = false
    var goal = false
    var compact = false
    var model = false
    var interrupt = false
    var steer = false
    var attach = false

    init(send: Bool = false, stream: Bool = false, reply: Bool = false, goal: Bool = false, compact: Bool = false, model: Bool = false, interrupt: Bool = false, steer: Bool = false, attach: Bool = false) {
        (self.send, self.stream, self.reply, self.goal, self.compact, self.model, self.interrupt, self.steer, self.attach) = (send, stream, reply, goal, compact, model, interrupt, steer, attach)
    }

    // A paired host on an older release omits attach; it cannot store uploads.
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        send = try container.decode(Bool.self, forKey: .send)
        stream = try container.decode(Bool.self, forKey: .stream)
        reply = try container.decode(Bool.self, forKey: .reply)
        goal = try container.decode(Bool.self, forKey: .goal)
        compact = try container.decode(Bool.self, forKey: .compact)
        model = try container.decode(Bool.self, forKey: .model)
        interrupt = try container.decode(Bool.self, forKey: .interrupt)
        steer = try container.decode(Bool.self, forKey: .steer)
        attach = try container.decodeIfPresent(Bool.self, forKey: .attach) ?? false
    }
}

struct SurfaceState: Decodable, Identifiable, Equatable {
    var id: String { name }
    let name: String
    let connected: Bool
    let error: String?
    let health: String
    let healthDetail: String?
    let capabilities: Capabilities
    var runtime: SurfaceRuntime? = nil
    var repairAction: String? = nil
    var repairLabel: String? = nil

    var needsAttention: Bool { health != "healthy" || !(runtime?.advice.isEmpty ?? true) }
}

struct SurfaceRuntimeNote: Decodable, Equatable {
    let problem: String
    let message: String
    let remediation: String?
}

struct SurfaceRuntime: Decodable, Equatable {
    let name: String
    let reachable: Bool
    let durable: Bool
    let problem: String?
    let detail: String?
    let remediation: String?
    let notes: [SurfaceRuntimeNote]?

    var advice: [String] {
        var lines: [String] = []
        if problem != nil {
            lines.append([detail, remediation.map { "Fix: \($0)" }].compactMap { $0 }.joined(separator: ". "))
        }
        for note in notes ?? [] {
            lines.append(note.remediation.map { "\(note.message). Fix: \($0)" } ?? note.message)
        }
        return lines
    }
}

struct SessionState: Codable, Identifiable, Hashable {
    let id: String
    let surface: String
    let name: String
    let alias: String?
    let status: String
    let lastActive: String?
    var queueCount: Int
    let open: Bool
    var current: Bool
    var currentReason: String?
    let capabilities: Capabilities
    let readOnly: Bool?
    let readOnlyReason: String?
    var cwd: String? = nil
    var hostProject: HostProjectIdentity? = nil
    var checkout: CheckoutIdentity? = nil
    var runtime: SessionRuntime? = nil
    var subagent: SubagentIdentity? = nil
    var subagents: SubagentRollup? = nil
    var hiddenSubagents: SubagentRollup? = nil
    var listed: Bool? = nil
    var host: SessionHost? = nil
    var unavailableReason: String? = nil
    var transport: String? = nil
    var waitingOn: WaitingState? = nil

    var displayName: String {
        if let alias, !alias.isEmpty { return "@\(alias)" }
        return name.isEmpty ? id : name
    }

    var isWorking: Bool { status == "busy" }
    var isWaitingOnYou: Bool { waitingOn != nil }
    var isReadOnly: Bool { readOnly == true }
    var remoteHostName: String? {
        guard let host, host.state != "local" else { return nil }
        return host.name
    }
    var isUnavailable: Bool { remoteHostName != nil && !(unavailableReason ?? "").isEmpty }
    /// The paired host that owns this session, or nil for this machine.
    var remoteHostID: String? { remoteHostName == nil ? nil : host?.id }
    var modelCatalogKey: String { "\(remoteHostID ?? "")/\(surface)" }
    var canCallByVoice: Bool { remoteHostID == nil }
}

struct SessionHost: Codable, Hashable {
    let id: String
    let name: String
    let state: String
}

struct HostHealth: Decodable, Hashable, Identifiable {
    let id: String
    let name: String
    let state: String
    var detail: String? = nil
    var notice: String? = nil

    var stateLabel: String {
        switch state {
        case "connected": return "Connected"
        case "connecting": return "Connecting"
        case "unreachable": return "Unreachable"
        case "unauthorized": return "Pairing revoked"
        case "incompatible": return "Needs update"
        default: return state.capitalized
        }
    }
    var isHealthy: Bool { state == "connected" }
}

/// A question, plan or approval the session is waiting on its user to answer.
struct WaitingState: Codable, Hashable {
    let kind: String
    let id: String
    var since: String? = nil
}

struct SubagentIdentity: Codable, Hashable {
    let parentId: String
    let rootId: String
    let depth: Int
    var nickname: String? = nil
    var role: String? = nil
}

struct SubagentRollup: Codable, Hashable {
    let count: Int
    let working: Int
}

struct SessionFamily: Identifiable, Equatable {
    struct Member: Identifiable, Equatable {
        let session: SessionState
        let depth: Int
        var id: String { session.id }
    }

    let root: SessionState
    let members: [Member]
    let subagentCount: Int
    let workingSubagents: Int

    var id: String { root.id }
    var isWorking: Bool { root.isWorking || workingSubagents > 0 }
    var isCurrent: Bool { isWorking || sessions.contains(where: \.current) }
    var sessions: [SessionState] { [root] + members.map(\.session) }

    func contains(_ sessionID: String?) -> Bool {
        guard let sessionID else { return false }
        return root.id == sessionID || members.contains { $0.id == sessionID }
    }
}

/// Refetches a session's subagents when it changes, its subagent counts do,
/// or the catalog reports a change among its hidden subagents.
struct SubagentFetchKey: Hashable {
    let sessionID: String
    let subagents: SubagentRollup?
    let hidden: SubagentRollup?
    let revision: Int

    init(_ session: SessionState, revision: Int) {
        sessionID = session.id
        subagents = session.subagents
        hidden = session.hiddenSubagents
        self.revision = revision
    }
}

/// The fetched subagent pages of the few sessions most recently shown; a
/// parent's page can run to hundreds of kilobytes.
struct SubagentPageCache {
    static let capacity = 4
    private(set) var errors: [String: String] = [:]
    private var rows: [String: [SessionState]] = [:]
    private var order: [String] = []
    private var revisions: [String: Int] = [:]

    func children(of parentID: String) -> [SessionState] { rows[parentID] ?? [] }
    func revision(_ parentID: String) -> Int { revisions[parentID] ?? 0 }
    func loading(_ parent: SessionState) -> Bool { (parent.hiddenSubagents?.count ?? 0) > 0 && rows[parent.id] == nil && errors[parent.id] == nil }
    func session(_ id: String) -> SessionState? { rows.values.lazy.flatMap { $0 }.first { $0.id == id } }

    mutating func store(_ sessions: [SessionState], for parentID: String) {
        rows[parentID] = sessions
        errors[parentID] = nil
        touch(parentID)
    }

    mutating func fail(_ message: String, for parentID: String) {
        errors[parentID] = message
        touch(parentID)
    }

    mutating func drop(_ parentID: String) {
        rows[parentID] = nil
        errors[parentID] = nil
        revisions[parentID] = nil
        order.removeAll { $0 == parentID }
    }

    /// A device's catalog stream reports a hidden subagent's arrival or change
    /// only as an unlisted removal without its parent, so an unknown one
    /// refetches every cached page.
    mutating func sessionRemoved(_ id: String, unlisted: Bool) {
        drop(id)
        let parents = rows.filter { $0.value.contains { $0.id == id } }.map(\.key)
        for parent in parents { rows[parent]?.removeAll { $0.id == id } }
        for parent in parents.isEmpty && unlisted ? Array(rows.keys) : parents {
            revisions[parent, default: 0] += 1
        }
    }

    private mutating func touch(_ parentID: String) {
        order.removeAll { $0 == parentID }
        order.append(parentID)
        while order.count > Self.capacity {
            let evicted = order.removeFirst()
            rows[evicted] = nil
            errors[evicted] = nil
            revisions[evicted] = nil
        }
    }
}

enum SessionFamilies {
    static func build(_ sessions: [SessionState]) -> [SessionFamily] {
        let ids = Set(sessions.map(\.id))
        var children: [String: [SessionState]] = [:]
        var roots: [SessionState] = []
        for session in sessions {
            if let parent = session.subagent?.parentId, parent != session.id, ids.contains(parent) {
                children[parent, default: []].append(session)
            } else {
                roots.append(session)
            }
        }
        var placed: Set<String> = []
        return roots.map { root in
            placed.insert(root.id)
            var members: [SessionFamily.Member] = []
            func walk(_ parentID: String, depth: Int) {
                for child in children[parentID] ?? [] where !placed.contains(child.id) {
                    placed.insert(child.id)
                    members.append(SessionFamily.Member(session: child, depth: depth))
                    walk(child.id, depth: depth + 1)
                }
            }
            walk(root.id, depth: 1)
            let observed = root.subagents ?? SubagentRollup(count: 0, working: 0)
            let counted = members.filter { $0.session.listed != false }
            return SessionFamily(
                root: root,
                members: members,
                subagentCount: counted.count + observed.count,
                workingSubagents: counted.filter { $0.session.isWorking }.count + observed.working
            )
        }
    }

    /// The direct subagents of a session: the fetched page, with any row the
    /// live session list holds taking the place of its fetched copy.
    static func children(of parentID: String, listed: [SessionState], fetched: [SessionState]) -> [SessionState] {
        let live = listed.filter { $0.subagent?.parentId == parentID }
        let liveIDs = Set(live.map(\.id))
        return fetched.filter { $0.subagent?.parentId == parentID && !liveIDs.contains($0.id) } + live
    }

    static func subagentSummary(_ family: SessionFamily) -> String {
        let noun = family.subagentCount == 1 ? "subagent" : "subagents"
        return family.workingSubagents > 0 ? "\(family.subagentCount) \(noun), \(family.workingSubagents) working" : "\(family.subagentCount) \(noun)"
    }

    static func label(_ session: SessionState) -> String {
        guard let nickname = session.subagent?.nickname, !nickname.isEmpty else { return session.title }
        if let role = session.subagent?.role, !role.isEmpty { return "\(nickname) (\(role))" }
        return nickname
    }

    static func title(_ session: SessionState, in sessions: [SessionState]) -> String {
        guard let parentID = session.subagent?.parentId else { return session.title }
        guard let parent = sessions.first(where: { $0.id == parentID }) else { return label(session) }
        return "\(title(parent, in: sessions)) > \(label(session))"
    }
}

extension SessionState {
    var title: String {
        if let alias, !alias.isEmpty { return "@\(alias)" }
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.isEmpty || trimmed == id || UUID(uuidString: trimmed) != nil { return "Untitled conversation" }
        return trimmed
    }
}

struct SessionRuntime: Codable, Hashable {
    struct Location: Codable, Hashable {
        var workspace: String? = nil
        var surface: String? = nil
        var session: String? = nil
        var pane: String? = nil
    }

    let launcher: String
    var location: Location? = nil
    var focusable: Bool? = nil

    var hostName: String? {
        switch launcher {
        case "cmux": return "cmux"
        case "tmux": return "tmux"
        default: return nil
        }
    }
}

struct GoalEditorState: Equatable {
    enum Mode: Equatable {
        case newGoal
        case editGoal
        case budget
    }

    private(set) var sessionID: String?
    private(set) var mode: Mode?

    mutating func begin(_ mode: Mode, sessionID: String) {
        self.sessionID = sessionID
        self.mode = mode
    }

    mutating func select(sessionID: String) {
        if self.sessionID != sessionID { reset() }
    }

    mutating func reset() {
        sessionID = nil
        mode = nil
    }

    func canCommit(currentSessionID: String?) -> Bool {
        mode != nil && sessionID != nil && sessionID == currentSessionID
    }
}

struct LauncherOption: Decodable, Identifiable, Hashable {
    let id: String
    let label: String
    let agents: [String]
    let available: Bool
    let detail: String?
    /// Whether the launcher applies every creation setting. A terminal launcher
    /// takes only the folder, message, model and name.
    let sessionOptions: Bool

    enum CodingKeys: String, CodingKey { case id, label, agents, available, detail, sessionOptions }

    init(id: String, label: String? = nil, agents: [String] = [], available: Bool = true, detail: String? = nil, sessionOptions: Bool = false) {
        self.id = id
        self.label = label ?? id
        self.agents = agents
        self.available = available
        self.detail = detail
        self.sessionOptions = sessionOptions
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(String.self, forKey: .id)
        label = try container.decode(String.self, forKey: .label)
        agents = try container.decodeIfPresent([String].self, forKey: .agents) ?? []
        available = try container.decode(Bool.self, forKey: .available)
        detail = try container.decodeIfPresent(String.self, forKey: .detail)
        sessionOptions = try container.decodeIfPresent(Bool.self, forKey: .sessionOptions) ?? false
    }

    var isTerminal: Bool { !sessionOptions }
}

struct HostProjectIdentity: Codable, Hashable {
    let id: String?
    let displayName: String?
    let commonDir: String?
    let path: String?
}

struct CheckoutIdentity: Codable, Hashable {
    let id: String?
    let path: String?
    let branch: String?
    let detachedHead: String?
    let isMain: Bool?
    let dirty: Bool?
}

struct QueueState: Decodable, Identifiable, Equatable {
	let operation: String?
    let sourceSessionId: String?
    let effort: String?
    let mode: String?
    let serviceTier: String?
    let outputSchema: RecordedJSON?
    let id: Int64
    let sessionId: String
    let target: String
    let message: String
    let model: String?
    let status: String
    let attempts: Int
    let lastError: String?
    let queuedAt: String
    var heldBy: String? = nil
    let historical: Bool?
    let evidence: String

    var isHistorical: Bool {
        historical == true || status == "delivered" || status == "canceled"
    }

    /// The queue stores queuedAt as SQLite's UTC "yyyy-MM-dd HH:mm:ss".
    var queuedDate: Date? { QueueState.queuedAtFormat.date(from: queuedAt) }

    private static let queuedAtFormat: DateFormatter = {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = TimeZone(identifier: "UTC")
        formatter.dateFormat = "yyyy-MM-dd HH:mm:ss"
        return formatter
    }()

    /// The send receipt for a queued message, worded as the daemon words it.
    var queuedReceipt: String {
        heldBy == "waiting"
            ? "Queued for \(target), which is waiting on you; sends after you answer."
            : "Queued for \(target); sends when current turn ends."
    }

    /// Why a pending message has not been delivered yet.
    var holdLabel: String {
        switch heldBy {
        case "waiting": return "Held: this agent is waiting on you"
        case "busy": return "Held: this agent is working"
        case "offline": return "Held: this agent is offline"
        case "unknown": return "Held: this agent's state is unknown"
        case "retrying": return "Held: waiting to retry"
        case "behind": return "Held: behind an earlier message"
        case "notLoaded": return "Held: this conversation is not loaded"
        case "hostUnreachable": return "Held: this agent's host is unreachable"
        default: return "Delivers on the next scan"
        }
    }
}

indirect enum RecordedJSON: Codable, Equatable {
    case object([String: RecordedJSON]), array([RecordedJSON]), string(String), number(Double), bool(Bool), null
    init(from decoder: Decoder) throws {
        let value = try decoder.singleValueContainer()
        if value.decodeNil() { self = .null }
        else if let item = try? value.decode(Bool.self) { self = .bool(item) }
        else if let item = try? value.decode(String.self) { self = .string(item) }
        else if let item = try? value.decode(Double.self) { self = .number(item) }
        else if let item = try? value.decode([String: RecordedJSON].self) { self = .object(item) }
        else { self = .array(try value.decode([RecordedJSON].self)) }
    }
    func encode(to encoder: Encoder) throws {
        var value = encoder.singleValueContainer()
        switch self {
        case .object(let item): try value.encode(item)
        case .array(let item): try value.encode(item)
        case .string(let item): try value.encode(item)
        case .number(let item): try value.encode(item)
        case .bool(let item): try value.encode(item)
        case .null: try value.encodeNil()
        }
    }
    var formatted: String {
        let encoder = JSONEncoder(); encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        return (try? encoder.encode(self)).flatMap { String(data: $0, encoding: .utf8) } ?? ""
    }
}

struct AttentionState: Decodable, Identifiable, Equatable {
    let id: Int64
    let sessionId: String
    let target: String
    let queueId: Int64
    let reason: String
    let requestedAction: String
    let createdAt: String
}

struct DeliveryProblem: Decodable, Identifiable, Equatable {
    let deliveryId: Int64
    let sessionId: String
    let sourceSessionId: String?
    let attentionSessionId: String?
    let message: String
    let reason: String
    let status: String?
    let at: String

    var id: Int64 { deliveryId }

    var reasonText: String {
        let words = reason.replacingOccurrences(of: "_", with: " ").trimmingCharacters(in: .whitespacesAndNewlines)
        guard let first = words.first else { return "The message was not delivered." }
        return first.uppercased() + words.dropFirst()
    }
}

struct ChannelState: Decodable, Identifiable, Equatable {
    var id: String { name }
    let name: String
    let members: [String]
    let memberDetails: [ChannelMemberState]?
}

struct ChannelMemberState: Decodable, Identifiable, Equatable {
    let id: String
    let display: String
}

struct RelayState: Decodable, Identifiable, Equatable {
    let handoff: String
    let pendingCount: Int64
    let lastError: String?
    let id: Int64
    let from: String
    let to: String
    let pattern: String
    let once: Bool
    let active: Bool
    let fireCount: Int64
    let lastFiredAt: String?
}

struct HistoryState: Decodable, Identifiable, Equatable {
    let id: Int64
    let createdAt: String
    let kind: String
    let sessionId: String?
    let sourceSessionId: String?
    let target: String?
    let source: String?
    let queueId: Int64?
    let message: String?
    let result: String?
    let error: String?
    var evidence: String? = nil
}

struct DaemonState: Decodable, Equatable {
    let running: Bool
    let pid: Int?
    let stale: Bool?
    let refreshError: String?
    var host: SessionHost? = nil
    var hosts: [HostHealth]? = nil

    /// The paired host a host-qualified session id names, for a session the
    /// snapshot has not listed yet.
    func pairedHost(owning sessionID: String) -> SessionHost? {
        guard let range = sessionID.range(of: "::") else { return nil }
        let hostID = String(sessionID[..<range.lowerBound])
        return hosts?.first { $0.id == hostID }.map { SessionHost(id: $0.id, name: $0.name, state: $0.state) }
    }
}

struct DashboardSnapshot: Decodable {
    let updatedAt: String
    let eventCursor: UInt64?
    let hostEpoch: String?
    let catalogSeq: UInt64?
    let daemon: DaemonState
    var surfaces: [SurfaceState]
    var sessions: [SessionState]
    var totalSessions: Int
    var nextCursor: String? = nil
    let queue: [QueueState]
    let channels: [ChannelState]
    let relays: [RelayState]
    let history: [HistoryState]
    let attention: [AttentionState]
    var deliveryProblems: [DeliveryProblem]?
    let codexRecentHours: Int
    let busyDelivery: String?
    var rekeyed: [String: String]? = nil

    func hasSamePresentation(as other: DashboardSnapshot) -> Bool {
        daemon == other.daemon &&
            surfaces == other.surfaces &&
            sessions == other.sessions &&
            totalSessions == other.totalSessions &&
            queue == other.queue &&
            channels == other.channels &&
            relays == other.relays &&
            history == other.history &&
            attention == other.attention &&
            deliveryProblems == other.deliveryProblems &&
            codexRecentHours == other.codexRecentHours &&
            busyDelivery == other.busyDelivery
    }
}

struct ExchangeState: Decodable, Identifiable {
    let user: String
    let assistant: String
    let timestamp: String
    var id: String { timestamp + user + assistant }
}

struct ContextState: Decodable {
    let usedTokens: Int64
    let contextWindow: Int64
    let cumulativeTokens: Int64?
    let compacting: Bool
    let compactionCount: Int
    let reclaimedTokens: Int64?
    let inputTokens: Int64?
    let cachedInputTokens: Int64?
    let outputTokens: Int64?
	let reasoningOutputTokens: Int64?
	let windowEstimated: Bool?
	let contextWindowSource: String?
	let updatedAt: String?
    let lastCompactedAt: String?

    var exceedsEstimatedWindow: Bool {
        windowEstimated == true && contextWindow > 0 && usedTokens > contextWindow
    }
	var fraction: Double? {
		guard contextWindow > 0, usedTokens <= contextWindow else { return nil }
		return Double(usedTokens) / Double(contextWindow)
	}
}

struct ModelOption: Decodable, Identifiable {
    let id: String
    let displayName: String
    let description: String?
    let `default`: Bool?
    let allowsCustom: Bool?
    let supportedReasoningEfforts: [String]?
    let defaultReasoningEffort: String?
}

struct SessionMetadata: Decodable {
    let context: ContextState?
    let goal: GoalState?
    let model: String?
    let models: [ModelOption]?
    let claudeRuns: [ClaudeRunObservation]?
    let claudeSubagents: [ClaudeSubagentLink]?
    let errors: [String: String]?
}

struct ClaudeRunObservation: Decodable, Identifiable, Equatable {
    let recordPath: String
    let jobId: String
    let sessionId: String?
    let resumeSessionId: String?
    let runType: String?
    let providerState: String?
    let createdAt: String?
    let updatedAt: String?

    var id: String { recordPath.isEmpty ? jobId : recordPath }
}

struct ClaudeSubagentLink: Decodable, Identifiable, Equatable {
    let parentSessionId: String
    let agentId: String
    var agentType: String? = nil
    var description: String? = nil
    var toolUseId: String? = nil
    var depth: Int? = nil
    var working: Bool? = nil
    var lastActive: String? = nil
    let transcriptPath: String

    var id: String { agentId + transcriptPath }
}

struct SessionDetail: Decodable {
    let session: RawSession
    let journalSeq: UInt64?
    let alias: String?
    let exchanges: [ExchangeState]
    let capabilities: Capabilities
    let readOnly: Bool
    let readOnlyReason: String
    var context: ContextState?
    var goal: GoalState?
    var model: String?
    var models: [ModelOption]?
    var claudeRuns: [ClaudeRunObservation]?
    var claudeSubagents: [ClaudeSubagentLink]?
    var metadataErrors: [String: String]?
    var timeline: SessionTimeline?
    let readSource: String?
    let readError: String?
    let transcriptWarning: String?
    let transcriptTruncated: Bool?
    let transcriptOriginalBytes: Int?
    let transcriptReturnedBytes: Int?
}

struct RawSession: Decodable {
    let id: String
    let surface: String
    let name: String
    let status: String
    let lastActive: String
    let source: String?
    let transport: String?
    let cwd: String?
    let runtime: SessionRuntime?
    var waitingOn: WaitingState? = nil
}

struct GoalState: Decodable {
    let objective: String
    let status: String
    let timeUsedSeconds: Int?
    let tokensUsed: Int?
    let tokenBudget: Int?
    let createdAt: String?
    let updatedAt: String?

    var displayStatus: String {
        switch status {
        case "active": return "Active"
        case "paused": return "Paused"
        case "blocked", "usageLimited", "budgetLimited": return "Needs you"
        case "complete": return "Complete"
        default: return status.capitalized
        }
    }

    var needsAttention: Bool {
        ["blocked", "usageLimited", "budgetLimited"].contains(status)
    }

    enum CodingKeys: String, CodingKey {
        case objective, status, timeUsedSeconds, tokensUsed, tokenBudget, createdAt, updatedAt
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        objective = try container.decode(String.self, forKey: .objective)
        status = try container.decode(String.self, forKey: .status)
        timeUsedSeconds = try container.decodeIfPresent(Int.self, forKey: .timeUsedSeconds)
        tokensUsed = try container.decodeIfPresent(Int.self, forKey: .tokensUsed)
        tokenBudget = try container.decodeIfPresent(Int.self, forKey: .tokenBudget)
        createdAt = try container.decodeIfPresent(String.self, forKey: .createdAt)
        updatedAt = try container.decodeIfPresent(String.self, forKey: .updatedAt)
    }
}

struct DeviceState: Codable, Identifiable {
    let id: String
    let name: String
    let scopes: [String]
    let createdAt: String
    let lastSeenAt: String?
    let revokedAt: String?
    let pushEnabled: Bool
}

struct DeviceListResponse: Decodable {
    let devices: [DeviceState]
}

struct RemoteAccessState: Decodable {
    let enabled: Bool
    let desired: Bool
    let provider: String
    let url: String?
    let dnsName: String?
    let port: Int
    let error: String?
}

struct DashboardConfigState: Decodable {
    let listen: String
    let codexRecentHours: Int
    let busyDelivery: String
}

struct DashboardSettingsState: Decodable {
    let dashboard: DashboardConfigState
    let remoteAccess: RemoteAccessState
    let notifications: NotificationStatusState
}

struct NotificationStatusState: Decodable {
    let enabled: Bool
    let available: Bool
    let authorization: String
    let authorized: Bool
    let alerts: Bool
    let sounds: Bool
    let error: String?
}

struct HistoryPageResponse: Decodable {
    let items: [HistoryState]
    let hasMore: Bool
    let nextBefore: Int64
    let kinds: [String]
}

struct SigninProfileState: Codable, Identifiable, Equatable {
    let id: String
    let label: String
    let browser: String
    let profile: String
    let createdAt: String
}

struct SigninConsumerState: Codable, Identifiable, Equatable {
    let id: String
    let label: String
    let purpose: String
    let profileId: String?
}

struct SigninDetectedProfile: Codable, Identifiable, Equatable {
    let profile: String
    let name: String
    let profileId: String?

    var id: String { profile }
    var choiceLabel: String { name == profile ? name : "\(name) (\(profile))" }
}

struct SigninBrowserState: Codable, Identifiable, Equatable {
    let browser: String
    let label: String
    let available: Bool
    let detail: String?
    let profiles: [SigninDetectedProfile]

    var id: String { browser }
    var addableProfiles: [SigninDetectedProfile] { profiles.filter { $0.profileId == nil } }
}

struct SigninState: Codable, Equatable {
    let profiles: [SigninProfileState]
    let consumers: [SigninConsumerState]
    let browsers: [SigninBrowserState]

    func browserLabel(_ browser: String) -> String {
        browsers.first { $0.browser == browser }?.label ?? browser
    }

    var addableBrowsers: [SigninBrowserState] { browsers.filter { $0.available && !$0.addableProfiles.isEmpty } }
    var unavailableBrowsers: [SigninBrowserState] { browsers.filter { !$0.available } }
}

struct SigninProfileResponse: Decodable {
    let profile: SigninProfileState?
}

struct PairingResponse: Decodable {
    let id: String
    let expiresAt: String
    let pairingURL: String
    let endpoint: String
    let secret: String
    let scopes: [String]
}

struct PairedDeviceResponse: Decodable {
    let device: DeviceState
    let token: String
    let `protocol`: Int
}

struct AgenthailEvent: Decodable {
    let id: UInt64
    let type: String
    let timestamp: String
    let entityId: String?
}

struct SessionTimeline: Decodable {
    let nextBefore: Int64?
    var items: [TimelineItem]
    let source: String?
    let truncated: Bool
    let unavailableReason: String?
}

struct SessionAttachment: Decodable, Equatable, Hashable {
    let id: String
    let mediaType: String
    let width: Int?
    let height: Int?
    let bytes: Int?

    var isImage: Bool { mediaType.hasPrefix("image/") }
}

// A file sent with a message that the agent received as a stored path. The
// path is on the host that owns the session.
struct AttachedFile: Decodable, Equatable, Hashable {
    let name: String
    var path = ""
    var bytes: Int64? = nil

    var sizeLabel: String? { bytes.map { ByteCountFormatter.string(fromByteCount: $0, countStyle: .file) } }
}

struct SessionStreamItem: Decodable {
    let itemId: String
    let version: UInt64
    let op: String
    let kind: String
    let turnId: String?
    let callId: String?
    let ts: String
    let body: String?
    let context: ContextState?
    let goal: GoalState?
    let role: String?
    let sender: String?
    let title: String?
    let status: String?
    let truncated: Bool
    let truncationReason: String?
    let bodyRef: String?
    let reason: String?
    let attachment: SessionAttachment?
    let files: [AttachedFile]?
}

struct SessionStreamBody: Decodable {
    let sessionId: String
    let bodyRef: String
    let start: Int
    let end: Int
    let total: Int
    let body: String
    let truncated: Bool
}

struct RetainedBodyResult {
    let text: String
    let error: String?
}

struct SessionStreamEvent: Decodable {
    let stream: String
    let sessionId: String
    let seq: UInt64
    let type: String
    let data: SessionStreamItem
}

struct CatalogStreamEvent: Decodable {
    let stream: String
    let seq: UInt64
    let type: String
    let data: CatalogStreamData
}

struct CatalogStreamData: Decodable {
    let session: SessionState?
    let sessionId: String?
    let surface: String?
    let health: String?
    let detail: String?
    let runtime: SurfaceRuntime?
    let deliveryId: Int64?
    let queueCount: Int?
    let sourceSessionId: String?
    let attentionSessionId: String?
    let message: String?
    let reason: String?
    let status: String?
    let at: String?
    let rekeyedTo: String?
}

struct TimelineItem: Decodable, Identifiable, Equatable {
    let id: String
    let kind: String
    let role: String?
    let title: String
    let text: String
    let timestamp: String?
    let callId: String?
    let status: String?
    let truncated: Bool
    let truncationReason: String?
    let bodyRef: String?
    var attachment: SessionAttachment? = nil
    var sender: String? = nil
    var files: [AttachedFile]? = nil

    var isPeerMessage: Bool { kind == "message" && role == "peer" }
    var peerSender: String { sender.flatMap { $0.isEmpty ? nil : $0 } ?? "another agent" }
    var peerLabel: String { "From \(peerSender)" }
}

struct SessionSearchResponse: Decodable {
    let results: [SessionSearchItem]
    let remoteError: String?
}
struct SessionSearchItem: Decodable, Identifiable {
    var id: String { session.id }
    let session: SessionState
    let snippet: String?
}

struct UploadedAttachment: Decodable, Equatable {
    let id: String
    let name: String
    let mediaType: String
    let bytes: Int64
}
