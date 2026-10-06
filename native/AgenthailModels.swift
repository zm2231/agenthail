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
    struct Surface: Decodable, Identifiable { let id: String; let workspace: Bool }
    let surfaces: [Surface]
    let workspaces: [String]
    var launchers: [LauncherOption]? = nil
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
    var sharedWith: [SharedProcess]? = nil

    var displayName: String {
        if let alias, !alias.isEmpty { return "@\(alias)" }
        return name.isEmpty ? id : name
    }

    var isWorking: Bool { status == "busy" }
    var isReadOnly: Bool { readOnly == true }
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

struct SharedProcess: Codable, Hashable, Identifiable {
    let id: String
    var name: String? = nil
    let pid: Int
    let status: String
    var startedAt: String? = nil
}

enum SharedConversation {
    static func peers(_ session: SessionState) -> [SharedProcess] {
        session.sharedWith ?? []
    }

    static func processCount(_ session: SessionState) -> Int {
        peers(session).isEmpty ? 0 : peers(session).count + 1
    }

    static func badge(_ session: SessionState) -> String? {
        let count = processCount(session)
        return count > 1 ? "⧉\(count)" : nil
    }

    static func badgeLabel(_ session: SessionState) -> String {
        "Open in \(processCount(session)) processes"
    }

    static func started(_ peer: SharedProcess) -> String {
        guard let raw = peer.startedAt, let date = parse(raw) else { return "start time unknown" }
        return "started \(date.formatted(date: .abbreviated, time: .shortened))"
    }

    static func note(_ session: SessionState) -> String? {
        let peers = peers(session)
        guard !peers.isEmpty else { return nil }
        if peers.count == 1 {
            return "Also open in pid \(peers[0].pid) (\(started(peers[0]))). Messages here go to this process."
        }
        return "Also open in \(peers.count) other processes. Messages here go to this process."
    }

    static func openLabel(_ peer: SharedProcess) -> String {
        "Open other process, pid \(peer.pid)"
    }

    static func peerLabel(_ peer: SharedProcess) -> String {
        "pid \(peer.pid) (\(started(peer)))"
    }

    private static func parse(_ raw: String) -> Date? {
        (try? Date.ISO8601FormatStyle(includingFractionalSeconds: true).parse(raw)) ?? (try? Date.ISO8601FormatStyle().parse(raw))
    }
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
            return SessionFamily(
                root: root,
                members: members,
                subagentCount: members.count + observed.count,
                workingSubagents: members.filter { $0.session.isWorking }.count + observed.working
            )
        }
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
    let expiresAt: Int64?
    let historical: Bool?
    let evidence: String

    var isHistorical: Bool {
        historical == true || status == "expired" || status == "delivered" || status == "canceled"
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
    let queue: [QueueState]
    let channels: [ChannelState]
    let relays: [RelayState]
    let history: [HistoryState]
    let attention: [AttentionState]
    var deliveryProblems: [DeliveryProblem]?
    let codexRecentHours: Int
    let busyDelivery: String?

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
    let message: String?
    let reason: String?
    let status: String?
    let at: String?
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
