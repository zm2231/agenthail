import Foundation

enum DecisionGroup: String, CaseIterable, Decodable, Sendable {
    case stopped, holding, waiting

    var title: String {
        switch self {
        case .stopped: "Stopped on you"
        case .holding: "Holding up work"
        case .waiting: "Waiting on you"
        }
    }
}

struct DecisionItem: Decodable, Identifiable, Hashable, Sendable {
    let group: DecisionGroup
    var sessionId: String
    var projectId: String?
    let kind: String
    var reason: String
    var since: String?
    let answerable: Bool
    var requests: [SessionRequest]?
    var requestError: String?
    var holdsUp: [String]?
    var workItem: String?
    var workTitle: String?
    var hostID: String?
    var covers: [DecisionItem] = []

    var id: String { isParked ? sessionId + "#?" + reason : key }
    var key: String { workItem.map { sessionId + "#" + $0 } ?? sessionId }
    var isParked: Bool { kind == "parked" }
    var coveredWork: [DecisionItem] { covers.isEmpty ? [self] : covers }
    var sinceDate: Date? { since.flatMap(TimestampParser.date) }
    var openRequests: [SessionRequest] { requests ?? [] }

    private enum CodingKeys: String, CodingKey {
        case group, sessionId, projectId, kind, reason, since, answerable, requests, requestError, holdsUp, workItem, workTitle
    }
}

extension SessionState {
    var inboxTitle: String? {
        guard let alias, !alias.isEmpty, !name.isEmpty else { return nil }
        let title = SessionNames.display(name)
        return title == displayName ? nil : title
    }
}

struct LeadHandledItem: Decodable, Identifiable, Hashable, Sendable {
    var sessionId: String
    var projectId: String?
    var heldBy: String
    let role: String
    let reason: String
    var since: String?

    var id: String { sessionId }
}

struct DecisionsPage: Decodable, Sendable {
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

    // A paired computer names this computer's sessions with this computer's host id, so they are read back as local ids.
    mutating func add(_ page: DecisionsPage, host hostID: String, own: String = "", qualify: (String) -> String) {
        func local(_ id: String) -> String { Self.localised(qualify(id), own: own) }
        let incoming = page.decisions.map { Self.qualified($0, host: hostID, local) }
        decisions = Self.grouped(Self.deduplicated(Self.ordered(decisions.flatMap(\.coveredWork) + incoming)))
        leadHandling += page.leadHandling.map { Self.qualified($0, local) }
    }

    static func localised(_ id: String, own: String) -> String {
        let prefix = own + "::"
        return !own.isEmpty && id.hasPrefix(prefix) ? String(id.dropFirst(prefix.count)) : id
    }

    static func owner(of id: String) -> String {
        id.range(of: "::").map { String(id[..<$0.lowerBound]) } ?? ""
    }

    static func deduplicated(_ items: [DecisionItem]) -> [DecisionItem] {
        var kept: [String: Int] = [:]
        var out: [DecisionItem] = []
        for item in items {
            if let index = kept[item.key] {
                if item.hostID == owner(of: item.sessionId) && out[index].hostID != owner(of: out[index].sessionId) { out[index] = item }
            } else {
                kept[item.key] = out.count
                out.append(item)
            }
        }
        return out
    }

    static func grouped(_ items: [DecisionItem]) -> [DecisionItem] {
        var lead: [String: Int] = [:]
        var out: [DecisionItem] = []
        for item in items {
            guard item.isParked else {
                out.append(item)
                continue
            }
            let key = item.sessionId + "\u{1}" + item.reason
            if let index = lead[key] {
                if out[index].covers.isEmpty { out[index].covers = [out[index]] }
                out[index].covers.append(item)
            } else {
                lead[key] = out.count
                out.append(item)
            }
        }
        return out
    }

    func removing(_ sessionID: String) -> DecisionInbox {
        var inbox = self
        inbox.decisions.removeAll { $0.sessionId == sessionID && $0.workItem == nil }
        return inbox
    }

    static func qualified(_ item: DecisionItem, host hostID: String, _ qualify: (String) -> String) -> DecisionItem {
        var item = item
        item.hostID = hostID
        if let work = item.workItem, item.reason.hasPrefix(work + " waits on you: ") {
            item.reason = String(item.reason.dropFirst(work.count + " waits on you: ".count))
        }
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
            default: return lhs.id < rhs.id
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

    static func dismissals(of item: DecisionItem) -> [(workItem: String?, requestID: String?, host: String)] {
        if item.isParked { return item.coveredWork.compactMap { entry in entry.workItem.map { ($0, nil, entry.hostID ?? "") } } }
        if item.answerable, !item.openRequests.isEmpty { return item.openRequests.map { (nil, $0.id, item.hostID ?? "") } }
        return [(nil, nil, item.hostID ?? "")]
    }

    static func settled(_ error: Error) -> Bool {
        if case AgenthailAPIError.requestStale = error { return true }
        if case AgenthailAPIError.request(404, _) = error { return true }
        return false
    }

    static func notNowTitle(_ item: DecisionItem) -> String {
        let count = item.answerable ? item.openRequests.count : 0
        return count > 1 ? "Not now for all \(count)" : "Not now"
    }

    func only(_ requestID: String, of item: DecisionItem) -> DecisionItem {
        var narrowed = item
        narrowed.requests = item.openRequests.filter { $0.id == requestID }
        return narrowed
    }

    static func reply(_ words: String, for item: DecisionItem) -> String {
        let ids = item.coveredWork.compactMap(\.workItem)
        return ids.isEmpty ? words : "Answer for \(ids.joined(separator: ", ")): \(words)"
    }

    static func tag(_ item: DecisionItem, sentTo name: String?) -> String? {
        let parts = [item.covers.count > 1 ? "Covers \(item.covers.count) items" : nil, name.map { "Sent to \($0)" }].compactMap { $0 }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
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
