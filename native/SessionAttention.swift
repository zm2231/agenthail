import Foundation

/// Why a session needs the person, worded for the row that shows it. Only a
/// session that can still act is flagged: archived and unavailable sessions
/// never are, whatever their records say.
struct SessionAttention: Equatable {
    enum Kind: CaseIterable {
        /// The agent waits on an answer, an approval, or input.
        case request
        /// A usage limit refused the session's last turn.
        case usageLimit
        /// A refusal or an API error ended the session's last turn.
        case stopped
        /// A lead a restart replaced still waits on a request; it is shown, but does not need the person.
        case replacedRequest
    }

    let kind: Kind
    let reason: String

    static func build(sessions: [SessionState]) -> [String: SessionAttention] {
        let live = Dictionary(sessions.map { ($0.id, $0) }, uniquingKeysWith: { first, _ in first }).filter { canAct($0.value) }
        var result: [String: SessionAttention] = [:]
        for session in live.values {
            if let waiting = session.waitingOn, session.replacedLead?.isEmpty == false {
                result[session.id] = SessionAttention(kind: .replacedRequest, reason: "From a replaced lead. " + requestReason(waiting.kind, place: session.waitingPlace ?? .elsewhere))
            } else if let waiting = session.waitingOn {
                result[session.id] = SessionAttention(kind: .request, reason: requestReason(waiting.kind, place: session.waitingPlace ?? .elsewhere))
            } else if session.pendingAskID != nil {
                let title = session.ask?.title ?? ""
                result[session.id] = SessionAttention(kind: .request, reason: title.isEmpty ? requestReason("question") : "Asks: \(title)")
            } else if let limit = session.limitLabel {
                result[session.id] = SessionAttention(kind: .usageLimit, reason: limit)
            } else if let stop = session.turnStop, SessionDisplayKind.of(session) == .stopped {
                result[session.id] = SessionAttention(kind: .stopped, reason: stop.detail)
            }
        }
        return result
    }

    static func canAct(_ session: SessionState) -> Bool {
        session.archived != true && !session.isUnavailable && !["unknown", "notLoaded"].contains(session.status)
    }

    static func requestReason(_ kind: String, place: WaitingPlace = .elsewhere) -> String {
        switch place {
        case .terminal: return "Waiting in its terminal"
        case .restart: return "Waiting on a prompt Agenthail can't reach. Stop and resume to clear it"
        default: break
        }
        switch kind {
        case "question": return "Asks you a question"
        case "input": return "Needs your input"
        case "dialog": return "Has a dialog open in its terminal"
        default: return "Needs your approval"
        }
    }
}
