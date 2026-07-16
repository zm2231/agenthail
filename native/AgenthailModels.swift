import Foundation

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

struct SurfaceState: Decodable, Identifiable {
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

    var displayName: String {
        if let alias, !alias.isEmpty { return "@\(alias)" }
        return name.isEmpty ? id : name
    }

    var isWorking: Bool { status == "busy" }
    var isReadOnly: Bool { readOnly == true }
}

struct QueueState: Decodable, Identifiable {
    let id: Int64
    let sessionId: String
    let target: String
    let message: String
    let model: String?
    let status: String
    let attempts: Int
    let lastError: String?
    let queuedAt: String
}

struct AttentionState: Decodable, Identifiable {
    let id: Int64
    let sessionId: String
    let target: String
    let queueId: Int64
    let reason: String
    let requestedAction: String
    let createdAt: String
}

struct ChannelState: Decodable, Identifiable {
    var id: String { name }
    let name: String
    let members: [String]
    let memberDetails: [ChannelMemberState]?
}

struct ChannelMemberState: Decodable, Identifiable {
    let id: String
    let display: String
}

struct RelayState: Decodable, Identifiable {
    let id: Int64
    let from: String
    let to: String
    let pattern: String
}

struct HistoryState: Decodable, Identifiable {
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

struct DaemonState: Decodable {
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
