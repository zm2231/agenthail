import Foundation

extension AgenthailIOSModel {
    var inboxStops: InboxStops {
        InboxStops.build(replacements: leadReplacements, inbox: decisionFeed.inbox)
    }

    var shownDecisions: DecisionInbox {
        var inbox = decisionFeed.inbox
        inbox.decisions = inboxStops.decisions
        return inbox
    }

    func sessionName(_ sessionID: String) -> String {
        session(sessionID)?.displayName ?? "Unknown session"
    }

    func sessionTitle(_ sessionID: String) -> String? { session(sessionID)?.inboxTitle }

    func projectOfSession() -> (String) -> String? {
        let projects = sessionProjects
        return { projects[$0] }
    }
}
