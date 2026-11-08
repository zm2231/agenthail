import Foundation

enum InboxStop: Identifiable, Equatable {
    case queued(AttentionState)
    case delivery(DeliveryProblem)
    case replacement(LeadReplacement)
    case decision(DecisionItem)

    var id: String {
        switch self {
        case .queued(let item): "queue:\(item.hostID ?? "")/\(item.queueId)"
        case .delivery(let item): "delivery:\(item.hostID ?? "")/\(item.deliveryId)"
        case .replacement(let item): "replacement:\(item.hostID ?? "")/\(item.id)"
        case .decision(let item): "decision:" + item.sessionId
        }
    }

    var sessionID: String {
        switch self {
        case .queued(let item): item.sessionId
        case .delivery(let item): item.sessionId
        case .replacement(let item): item.sessionId
        case .decision(let item): item.sessionId
        }
    }

    func title(name: (String) -> String) -> String {
        switch self {
        case .replacement(let item): "@\(item.name) now runs in a new conversation"
        case .queued, .delivery, .decision: name(sessionID)
        }
    }

    var summary: String {
        switch self {
        case .queued(let item): item.reason
        case .delivery(let item): item.reasonText
        case .replacement(let item): item.reason
        case .decision(let item): item.reason
        }
    }

    var lines: [String] {
        switch self {
        case .queued(let item): [item.reason, item.requestedAction]
        case .delivery(let item): [item.reasonText, item.message]
        case .replacement(let item): ["Replaced by \(item.changedBy) because \(item.reason)"]
        case .decision(let item): [item.reason]
        }
    }

    var at: String? {
        switch self {
        case .queued(let item): item.createdAt
        case .delivery(let item): item.at
        case .replacement(let item): item.at
        case .decision(let item): item.since
        }
    }
}

struct InboxStops: Equatable {
    var stopped: [InboxStop]
    var decisions: [DecisionItem]

    var count: Int { stopped.count + decisions.filter { $0.group != .stopped }.count }

    // Where a computer's own delivery rows are listed, they stand in for its incoming/outgoing decisions.
    static func build(attention: [AttentionState], deliveries: [DeliveryProblem], replacements: [LeadReplacement], inbox: DecisionInbox, localHosts: Set<String>) -> InboxStops {
        let decisions = inbox.decisions.filter { item in
            !(item.group == .stopped && (item.kind == "incoming" || item.kind == "outgoing") && localHosts.contains(item.hostID ?? ""))
        }
        let stopped = attention.map(InboxStop.queued) + deliveries.map(InboxStop.delivery) + replacements.map(InboxStop.replacement)
            + decisions.filter { $0.group == .stopped }.map(InboxStop.decision)
        return InboxStops(stopped: stopped, decisions: decisions)
    }

    var rows: [InboxStop] {
        stopped.filter { if case .decision = $0 { false } else { true } } + decisions.map(InboxStop.decision)
    }

    static func projectID(of stop: InboxStop, in projects: [ProjectState], projectOf session: (String) -> String?) -> String? {
        func placed(_ sessionID: String, project: String?, host: String?) -> DecisionItem {
            var item = DecisionItem(group: .stopped, sessionId: sessionID, projectId: project ?? session(sessionID), kind: "", reason: "", answerable: false)
            item.hostID = host
            return item
        }
        let item: DecisionItem = switch stop {
        case .queued(let row): placed(row.sessionId, project: nil, host: row.hostID)
        case .delivery(let row): placed(row.sessionId, project: nil, host: row.hostID)
        case .replacement(let row): placed(row.sessionId, project: row.projectId, host: row.hostID)
        case .decision(let row): row
        }
        return DecisionInbox.projectID(of: item, in: projects)
    }

    func needsYou(in projects: [ProjectState], projectOf session: (String) -> String?) -> [String: Int] {
        rows.reduce(into: [:]) { counts, stop in
            if let project = Self.projectID(of: stop, in: projects, projectOf: session) { counts[project, default: 0] += 1 }
        }
    }

    func rows(in project: String, of projects: [ProjectState], projectOf session: (String) -> String?) -> [InboxStop] {
        rows.filter { Self.projectID(of: $0, in: projects, projectOf: session) == project }
    }
}
