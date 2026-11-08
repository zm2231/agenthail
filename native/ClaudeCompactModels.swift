import Foundation

struct ClaudeCompactWindow: Decodable, Equatable {
    var tokens: Int64?
    var auto: Bool?
    var ignored: String?
}

struct ClaudeIdleCompaction: Decodable, Equatable {
    let enabled: Bool
    let minContextTokens: Int64
}

struct ClaudeCompactRoot: Decodable, Identifiable, Equatable {
    let root: String
    let settingsPath: String
    let autoCompactWindow: ClaudeCompactWindow?
    let modelWindows: [String: ClaudeCompactWindow]?
    let autoCompactEnabled: Bool?
    let idleCompaction: Bool?
    let agenthailIdleCompaction: ClaudeIdleCompaction
    let overrides: [String: String]?

    var id: String { root }
}

struct ClaudeCompactHold: Decodable, Equatable {
    let sessionId: String
    let session: String?
    let root: String
    let contextTokens: Int64
    let reason: String
    let at: String
}

struct ClaudeCompactResponse: Decodable {
    let roots: [ClaudeCompactRoot]
    let idleCompactionHolds: [ClaudeCompactHold]?
}
