import Foundation

struct ProjectContents: Equatable {
    var lead: ProjectState.Lead?
    var leadSession: SessionState?
    var running: [SessionState] = []
    var recent: [SessionState] = []
    var earlier: [SessionState] = []

    static func key(of project: ListedProject) -> String { HostCatalog.qualify(project.hostID, project.state.id) }

    static func leadSessionID(_ lead: ProjectState.Lead, of project: ListedProject) -> String? {
        guard let sessionID = lead.sessionId, !sessionID.isEmpty else { return nil }
        let owner = lead.host.flatMap { $0.state == "local" ? nil : $0.id } ?? project.hostID
        return HostCatalog.qualify(owner, sessionID)
    }

    static func resumable(_ session: SessionState) -> Bool { !session.isWorking && !session.isUnavailable }

    static func running(_ session: SessionState) -> Bool {
        session.isWorking || session.waitingOn != nil || session.queueCount > 0
    }

    static func build(_ project: ListedProject, sessions: [SessionState]) -> ProjectContents {
        let key = key(of: project)
        var contents = ProjectContents(lead: project.state.leads.first)
        let leadID = contents.lead.flatMap { leadSessionID($0, of: project) }
        contents.leadSession = leadID.flatMap { id in sessions.first { $0.id == id } }
        let members = sessions.filter { $0.project?.id == key && $0.subagent == nil && $0.archived != true && $0.id != leadID }
        contents.running = members.filter(running)
        contents.recent = members.filter { $0.current && !running($0) }
        contents.earlier = members.filter { !$0.current && !running($0) }.sorted { ($0.lastActive ?? "") > ($1.lastActive ?? "") }
        return contents
    }
}
