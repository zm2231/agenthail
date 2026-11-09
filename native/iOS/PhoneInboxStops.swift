import Foundation

extension AgenthailIOSModel {
    var inboxStops: InboxStops {
        InboxStops.build(attention: attention, deliveries: deliveryProblems, replacements: leadReplacements,
                         inbox: decisionFeed.inbox, localHosts: Set(hosts.map(\.id)))
    }

    var shownDecisions: DecisionInbox {
        var inbox = decisionFeed.inbox
        inbox.decisions = inboxStops.decisions
        return inbox
    }

    func sessionName(_ sessionID: String) -> String {
        session(sessionID)?.displayName ?? sessionID
    }

    func projectOfSession() -> (String) -> String? {
        let projects = sessionProjects
        return { projects[$0] }
    }
}
