import Foundation

enum DecisionGroup: String, CaseIterable, Decodable {
    case stopped, holding, waiting

    var title: String {
        switch self {
        case .stopped: "Stopped on you"
        case .holding: "Holding up work"
        case .waiting: "Waiting on you"
        }
    }
}

struct DecisionItem: Decodable, Identifiable, Hashable {
    let group: DecisionGroup
    var sessionId: String
    var projectId: String?
    let kind: String
    let reason: String
    var since: String?
    let answerable: Bool
    var requests: [SessionRequest]?
    var requestError: String?
    var holdsUp: [String]?
    var hostID: String?

    var id: String { sessionId }
    var sinceDate: Date? { since.flatMap(TimestampParser.date) }
    var openRequests: [SessionRequest] { requests ?? [] }

    private enum CodingKeys: String, CodingKey {
        case group, sessionId, projectId, kind, reason, since, answerable, requests, requestError, holdsUp
    }
}

struct LeadHandledItem: Decodable, Identifiable, Hashable {
    var sessionId: String
    var projectId: String?
    var heldBy: String
    let role: String
    let reason: String
    var since: String?

    var id: String { sessionId }
}

struct DecisionsPage: Decodable {
    var decisions: [DecisionItem]
    var leadHandling: [LeadHandledItem]
}
