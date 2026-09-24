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
}
struct CreationModels: Decodable { let models: [ModelOption] }
struct QueueResponse: Decodable { let items: [QueueState] }
struct SessionCreationReceipt: Decodable {
    let ok: Bool
    let unknown: Bool?
    let session: RawSession?
    let sessionId: String?
    let error: String?
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
}

struct SessionState: Codable, Identifiable, Hashable {
    let id: String
    let surface: String
    let name: String
    let alias: String?
    let status: String
    let lastActive: String?
    let queueCount: Int
    let open: Bool
    let current: Bool
    let currentReason: String?
    let capabilities: Capabilities
    let readOnly: Bool?
    let readOnlyReason: String?
    var cwd: String? = nil

    var displayName: String {
        if let alias, !alias.isEmpty { return "@\(alias)" }
        return name.isEmpty ? id : name
    }

    var isWorking: Bool { status == "busy" }
    var isReadOnly: Bool { readOnly == true }
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
    let daemon: DaemonState
    let surfaces: [SurfaceState]
    let sessions: [SessionState]
    let totalSessions: Int
    let queue: [QueueState]
    let channels: [ChannelState]
    let relays: [RelayState]
    let history: [HistoryState]
    let attention: [AttentionState]
    let codexRecentHours: Int

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
            codexRecentHours == other.codexRecentHours
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
    let updatedAt: String?
    let lastCompactedAt: String?

    var fraction: Double {
        guard contextWindow > 0 else { return 0 }
        return min(1, Double(usedTokens) / Double(contextWindow))
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

struct SessionDetail: Decodable {
    let session: RawSession
    let alias: String?
    let exchanges: [ExchangeState]
    let capabilities: Capabilities
    let readOnly: Bool
    let readOnlyReason: String
    let context: ContextState?
    let goal: GoalState?
    let model: String?
    let models: [ModelOption]?
    let timeline: SessionTimeline?
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
}

struct GoalState: Decodable {
    let objective: String
    let status: String
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

struct DashboardSettingsState: Decodable {
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

enum AppSection: String, CaseIterable, Identifiable {
    case overview = "Overview"
    case conversations = "Conversations"
    case operations = "Operations"
    var id: String { rawValue }
    var symbol: String {
        switch self {
        case .overview: return "square.grid.2x2"
        case .conversations: return "bubble.left.and.bubble.right"
        case .operations: return "slider.horizontal.3"
        }
    }
}

struct SessionTimeline: Decodable {
    let nextBefore: Int64?
    let items: [TimelineItem]
    let source: String?
    let truncated: Bool
    let unavailableReason: String?
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
