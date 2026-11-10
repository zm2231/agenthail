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

enum DecisionAnswer: Equatable {
    case answered
    case failed(RequestAnswerFailure)
    case busy
}

struct DecisionInbox: Equatable {
    var decisions: [DecisionItem] = []
    var leadHandling: [LeadHandledItem] = []
    var unreachable: [String] = []
    var outdated: [String] = []

    var isEmpty: Bool { decisions.isEmpty && leadHandling.isEmpty }

    func items(in group: DecisionGroup) -> [DecisionItem] {
        decisions.filter { $0.group == group }
    }

    func decision(answering askID: String?) -> DecisionItem? {
        guard let askID, !askID.isEmpty else { return nil }
        return decisions.first { $0.answerable && $0.openRequests.contains { $0.id == askID } }
    }

    mutating func add(_ page: DecisionsPage, host hostID: String, qualify: (String) -> String) {
        decisions = Self.ordered(decisions + page.decisions.map { Self.qualified($0, host: hostID, qualify) })
        leadHandling += page.leadHandling.map { Self.qualified($0, qualify) }
    }

    func removing(_ sessionID: String) -> DecisionInbox {
        var inbox = self
        inbox.decisions.removeAll { $0.sessionId == sessionID }
        return inbox
    }

    static func qualified(_ item: DecisionItem, host hostID: String, _ qualify: (String) -> String) -> DecisionItem {
        var item = item
        item.hostID = hostID
        item.sessionId = qualify(item.sessionId)
        item.projectId = item.projectId.map(qualify)
        item.holdsUp = item.holdsUp?.map(qualify)
        return item
    }

    static func qualified(_ item: LeadHandledItem, _ qualify: (String) -> String) -> LeadHandledItem {
        var item = item
        item.sessionId = qualify(item.sessionId)
        item.projectId = item.projectId.map(qualify)
        item.heldBy = qualify(item.heldBy)
        return item
    }

    static func ordered(_ items: [DecisionItem]) -> [DecisionItem] {
        let rank = Dictionary(uniqueKeysWithValues: DecisionGroup.allCases.enumerated().map { ($1, $0) })
        return items.sorted { lhs, rhs in
            if lhs.group != rhs.group { return rank[lhs.group]! < rank[rhs.group]! }
            switch (lhs.sinceDate, rhs.sinceDate) {
            case let (left?, right?) where left != right: return left < right
            default: return lhs.sessionId < rhs.sessionId
            }
        }
    }

    // A paired computer names a project as it knows it, so its decision reaches the tile that holds that checkout or link.
    static func projectID(of item: DecisionItem, in projects: [ProjectState]) -> String? {
        guard let named = item.projectId, !named.isEmpty else { return nil }
        let host = item.hostID ?? ""
        let prefix = host + "::"
        let id = !host.isEmpty && named.hasPrefix(prefix) ? String(named.dropFirst(prefix.count)) : named
        if projects.contains(where: { $0.id == named }) { return named }
        guard !host.isEmpty else { return nil }
        let names: Set<String> = [named, id]
        return projects.first { project in
            project.checkouts.contains { $0.host.id == host && (names.contains($0.projectId) || $0.linkedFrom.map { names.contains($0.id) } == true) }
                || project.linked?.contains { names.contains($0.id) } == true
        }?.id
    }

    func needsYou(in projects: [ProjectState]) -> [String: Int] {
        decisions.reduce(into: [:]) { counts, item in
            if let project = Self.projectID(of: item, in: projects) { counts[project, default: 0] += 1 }
        }
    }

    func decisions(in projectID: String, of projects: [ProjectState]) -> [DecisionItem] {
        decisions.filter { Self.projectID(of: $0, in: projects) == projectID }
    }

    // Offered only when every question names a suggestion, so accepting never guesses.
    static func recommended(_ request: SessionRequest) -> RequestDecision? {
        guard request.usesPager, let questions = request.questions, !questions.isEmpty else { return nil }
        var choices: [String] = []
        for question in questions {
            guard let suggested = question.suggested, question.options.indices.contains(suggested) else { return nil }
            choices.append(question.options[suggested].id)
        }
        return RequestDecision(choices: choices)
    }
}
