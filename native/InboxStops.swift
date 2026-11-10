import Foundation

enum InboxStop: Identifiable, Equatable {
    case replacement(LeadReplacement)
    case decision(DecisionItem)

    var id: String {
        switch self {
        case .replacement(let item): "replacement:\(item.hostID ?? "")/\(item.id)"
        case .decision(let item): "decision:" + item.sessionId
        }
    }

    var sessionID: String {
        switch self {
        case .replacement(let item): item.sessionId
        case .decision(let item): item.sessionId
        }
    }

    func title(name: (String) -> String) -> String {
        switch self {
        case .replacement(let item): "@\(item.name) now runs in a new conversation"
        case .decision: name(sessionID)
        }
    }

    var summary: String {
        switch self {
        case .replacement(let item): item.reason
        case .decision(let item): item.reason
        }
    }

    var lines: [String] {
        switch self {
        case .replacement(let item): ["Replaced by \(item.changedBy) because \(item.reason)"]
        case .decision(let item): [item.reason]
        }
    }

    var at: String? {
        switch self {
        case .replacement(let item): item.at
        case .decision(let item): item.since
        }
    }
}

struct InboxStops: Equatable {
    var stopped: [InboxStop]
    var decisions: [DecisionItem]

    var count: Int { stopped.count + decisions.filter { $0.group != .stopped }.count }

    static func build(replacements: [LeadReplacement], inbox: DecisionInbox) -> InboxStops {
        let stopped = replacements.map(InboxStop.replacement) + inbox.decisions.filter { $0.group == .stopped }.map(InboxStop.decision)
        return InboxStops(stopped: stopped, decisions: inbox.decisions)
    }

    var rows: [InboxStop] {
        stopped.filter { if case .decision = $0 { false } else { true } } + decisions.map(InboxStop.decision)
    }

    static func projectID(of stop: InboxStop, in projects: [ProjectState], projectOf session: (String) -> String?) -> String? {
        switch stop {
        case .replacement(let row):
            var item = DecisionItem(group: .stopped, sessionId: row.sessionId, projectId: row.projectId ?? session(row.sessionId), kind: "", reason: "", answerable: false)
            item.hostID = row.hostID
            return DecisionInbox.projectID(of: item, in: projects)
        case .decision(let row):
            return DecisionInbox.projectID(of: row, in: projects)
        }
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
