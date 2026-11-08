import Foundation

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

    mutating func add(_ page: DecisionsPage, host hostID: String) {
        decisions = Self.ordered(decisions + page.decisions.map { Self.qualified($0, host: hostID) })
        leadHandling += page.leadHandling.map { Self.qualified($0, host: hostID) }
    }

    func removing(_ sessionID: String) -> DecisionInbox {
        var inbox = self
        inbox.decisions.removeAll { $0.sessionId == sessionID }
        return inbox
    }

    static func qualified(_ item: DecisionItem, host hostID: String) -> DecisionItem {
        var item = item
        item.hostID = hostID
        item.sessionId = HostCatalog.qualify(hostID, item.sessionId)
        item.projectId = item.projectId.map { HostCatalog.qualify(hostID, $0) }
        item.holdsUp = item.holdsUp?.map { HostCatalog.qualify(hostID, $0) }
        return item
    }

    static func qualified(_ item: LeadHandledItem, host hostID: String) -> LeadHandledItem {
        var item = item
        item.sessionId = HostCatalog.qualify(hostID, item.sessionId)
        item.projectId = item.projectId.map { HostCatalog.qualify(hostID, $0) }
        item.heldBy = HostCatalog.qualify(hostID, item.heldBy)
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

    // One unreachable computer must not hold the decisions, so computers are asked at once with a short timeout.
    static func load(from hosts: [InboxPage.Host]) async -> DecisionInbox {
        await withTaskGroup(of: (InboxPage.Host, Result<DecisionsPage, Error>).self) { group in
            for host in hosts {
                group.addTask {
                    do { return (host, .success(try await host.api.decisions(host: host.remote, timeout: InboxPage.hostTimeout))) }
                    catch { return (host, .failure(error)) }
                }
            }
            var inbox = DecisionInbox()
            for await (host, result) in group {
                switch result {
                case .success(let page): inbox.add(page, host: host.id)
                case .failure(AgenthailAPIError.olderHost): inbox.outdated = (inbox.outdated + [host.name]).sorted()
                case .failure: inbox.unreachable = (inbox.unreachable + [host.name]).sorted()
                }
            }
            return inbox
        }
    }
}
