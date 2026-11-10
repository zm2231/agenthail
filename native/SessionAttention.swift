import Foundation

struct RowAttention: Codable, Hashable, Sendable {
    let kind: String
    let reason: String
}

struct SessionAttention: Equatable {
    enum Kind: CaseIterable {
        case request
        case usageLimit
        case stopped
        case replacedRequest
    }

    let kind: Kind
    let reason: String

    init(kind: Kind, reason: String) {
        self.kind = kind
        self.reason = reason
    }

    init(_ row: RowAttention) {
        switch row.kind {
        case "request", "ask": kind = .request
        case "usageLimit": kind = .usageLimit
        case "replacedRequest": kind = .replacedRequest
        default: kind = .stopped
        }
        reason = row.reason
    }

    static func of(_ sessions: [SessionState]) -> [String: SessionAttention] {
        sessions.reduce(into: [:]) { result, session in
            if result[session.id] == nil, let attention = session.attention { result[session.id] = SessionAttention(attention) }
        }
    }
}
