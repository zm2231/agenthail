import Foundation

struct ProjectContents: Equatable {
    var lead: ProjectState.Lead?
    var leadSession: SessionState?
    var running: [SessionState] = []
    var recent: [SessionState] = []
    var earlier: [SessionState] = []

    static func resumable(_ session: SessionState) -> Bool { !session.isWorking && !session.isUnavailable }

    static func running(_ session: SessionState) -> Bool {
        session.isWorking || session.waitingOn != nil || session.queueCount > 0
    }

    static func build(lead: ProjectState.Lead?, leadID: String?, projectID: String, sessions: [SessionState]) -> ProjectContents {
        var contents = ProjectContents(lead: lead)
        contents.leadSession = leadID.flatMap { id in sessions.first { $0.id == id } }
        let members = sessions.filter { $0.project?.id == projectID && $0.subagent == nil && $0.archived != true && $0.id != leadID }
        contents.running = members.filter(running)
        contents.recent = members.filter { $0.current && !running($0) }
        contents.earlier = members.filter { !$0.current && !running($0) }.sorted { ($0.lastActive ?? "") > ($1.lastActive ?? "") }
        return contents
    }
}
