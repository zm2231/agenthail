import Foundation

struct ProjectContents: Equatable {
    var lead: ProjectState.Lead?
    var leadSession: SessionState?
    var now = ProjectNow()
    var earlier: [SessionState] = []

    enum LeadAction: Equatable {
        case resume(enabled: Bool, lifecycle: Bool)
        case open
        case message
    }

    static func leadSessionID(_ lead: ProjectState.Lead) -> String? {
        guard let sessionID = lead.sessionId, !sessionID.isEmpty else { return nil }
        guard let host = lead.host, host.state != "local", !sessionID.contains("::") else { return sessionID }
        return host.id + "::" + sessionID
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

    static let recentWindow: TimeInterval = 24 * 60 * 60

    static func isNow(_ session: SessionState, at now: Date) -> Bool {
        if running(session) || session.queueCount > 0 || session.waitingOn != nil || session.pendingAskID != nil || session.attention != nil { return true }
        guard let raw = session.lastActive, let last = TimestampParser.date(raw) else { return false }
        return now.timeIntervalSince(last) <= recentWindow
    }

    static func build(lead: ProjectState.Lead?, leadID: String?, projectID: String, sessions: [SessionState], computers: Int, localName: String = "this computer", now: Date = Date()) -> ProjectContents {
        var contents = ProjectContents(lead: lead)
        contents.leadSession = leadID.flatMap { id in sessions.first { $0.id == id } }
        let members = sessions.filter { $0.project?.id == projectID && $0.subagent == nil && $0.archived != true && $0.id != leadID }
        contents.now = ProjectNow(members.filter { isNow($0, at: now) }, computers: computers, localName: localName)
        contents.earlier = members.filter { !isNow($0, at: now) }
        return contents
    }
}
