import Foundation

struct InboxMessagePage: Decodable {
    var items: [InboxMessage]
    var nextBefore: Int64?
}

struct InboxMessage: Decodable, Identifiable, Equatable {
    let id: Int64
    var sessionId: String
    let target: String
    var sourceSessionId: String?
    let source: String?
    let message: String
    var truncated: Bool? = nil
    let status: String
    let evidence: String
    let pending: Bool
    var failure: String? = nil
    var queueId: Int64? = nil
    var queueStatus: String? = nil
    var attempts: Int? = nil
    var lastError: String? = nil
    let createdAt: String
    var hostID: String? = nil

    private enum CodingKeys: String, CodingKey {
        case id, sessionId, target, sourceSessionId, source, message, truncated, status, evidence, pending, failure, queueId, queueStatus, attempts, lastError, createdAt
    }

    var listID: String { "\(hostID ?? "")/\(id)" }
    var createdDate: Date? { TimestampParser.date(createdAt) }
    var reason: String? { [lastError, failure].compactMap { $0 }.first { !$0.isEmpty } }

    var senderLabel: String? {
        switch sourceSessionId?.components(separatedBy: "::").last {
        case "agenthail-person": return "you"
        case "agenthail-cli": return "the command line or a script"
        case "agenthail": return "Agenthail"
        default: return source.flatMap { $0.isEmpty ? nil : $0 }
        }
    }

    var outcomeLabel: String {
        switch evidence {
        case "delivered", "reply_observed": return "Delivered"
        case "transport_accepted": return "Transport accepted"
        case "unknown": return "Never confirmed"
        case "failed": return "Delivery failed"
        case "expired": return "Expired"
        case "canceled": return "Canceled"
        default: return status.capitalized
        }
    }
}
