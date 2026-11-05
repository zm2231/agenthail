import Foundation

struct ProjectContents: Equatable {
    var lead: ProjectState.Lead?
    var leadSession: SessionState?
    var running: [SessionState] = []
    var recent: [SessionState] = []
    var earlier: [SessionState] = []

    enum LeadAction: Equatable {
        case resume(enabled: Bool, lifecycle: Bool)
        case open
        case message
    }

    static func leadNote(_ lead: ProjectState.Lead) -> String {
        (lead.sessionId ?? "").isEmpty ? "starts with the first message sent to it" : "not running · the next message picks it up"
    }

    static func resumable(_ session: SessionState) -> Bool { !session.isWorking && !session.isUnavailable }

    static func leadActions(_ session: SessionState) -> [LeadAction] {
        if session.isWorking { return [.open, .message] }
        return [.resume(enabled: resumable(session), lifecycle: resumable(session) && session.capabilities.lifecycle.resume), .open]
    }

    static func running(_ session: SessionState) -> Bool {
        session.archived != true && (session.isWorking || (session.subagents?.working ?? 0) > 0)
    }

    static func build(lead: ProjectState.Lead?, leadID: String?, projectID: String, sessions: [SessionState]) -> ProjectContents {
        var contents = ProjectContents(lead: lead)
        contents.leadSession = leadID.flatMap { id in sessions.first { $0.id == id } }
        let members = sessions.filter { $0.project?.id == projectID && $0.subagent == nil && $0.archived != true && $0.id != leadID }
        let idle = members.filter { !running($0) }
            .map { (session: $0, active: $0.lastActive.flatMap(SessionTree.parseTimestamp) ?? .distantPast) }
            .sorted { $0.active > $1.active }
            .map(\.session)
        contents.running = members.filter(running)
        contents.recent = idle.filter(\.current)
        contents.earlier = idle.filter { !$0.current }
        return contents
    }
}
