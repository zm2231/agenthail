import Foundation

extension AgenthailIOSModel {
    var inboxStops: InboxStops {
        InboxStops.build(attention: snapshot?.attention ?? [], deliveries: deliveryProblems, replacements: leadReplacements,
                         inbox: decisionFeed.inbox, localHosts: Set(hosts.map(\.id)))
    }

    var shownDecisions: DecisionInbox {
        var inbox = decisionFeed.inbox
        inbox.decisions = inboxStops.decisions
        return inbox
    }

    func sessionName(_ sessionID: String) -> String {
        snapshot?.sessions.first { $0.id == sessionID }?.displayName ?? sessionID
    }

    func projectOfSession() -> (String) -> String? {
        let projects = Dictionary((snapshot?.sessions ?? []).compactMap { session in session.project.map { (session.id, $0.id) } }, uniquingKeysWith: { first, _ in first })
        return { projects[$0] }
    }
}
