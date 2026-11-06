import Foundation

/// Why a session needs the person, worded for the row that shows it. Only a
/// session that can still act is flagged: archived and unavailable sessions
/// never are, whatever their records say.
struct SessionAttention: Equatable {
    enum Kind: CaseIterable {
        /// The agent waits on an answer, an approval, or input.
        case request
        /// A message to this session failed and needs a decision.
        case incoming
        /// Messages this session sent were not delivered.
        case outgoing
        /// A usage limit refused the session's last turn.
        case usageLimit
        /// A refusal or an API error ended the session's last turn.
        case stopped
        /// A lead a restart replaced still waits on a request; it is shown, but does not need the person.
        case replacedRequest
    }

    let kind: Kind
    let reason: String

    static func build(sessions: [SessionState], attention: [AttentionState], deliveryProblems: [DeliveryProblem]) -> [String: SessionAttention] {
        let all = Dictionary(sessions.map { ($0.id, $0) }, uniquingKeysWith: { first, _ in first })
        let live = all.filter { canAct($0.value) }
        var result: [String: SessionAttention] = [:]
        let outgoing = Dictionary(grouping: deliveryProblems.filter { $0.attentionSessionId.map { live[$0] != nil } ?? false }, by: { $0.attentionSessionId! })
        for (id, problems) in outgoing {
            let targets = Set(problems.map(\.sessionId))
            let count = problems.count == 1 ? "1 message" : "\(problems.count) messages"
            let target = targets.count == 1 ? all[targets.first!].map { " to \($0.displayName)" } ?? "" : ""
            result[id] = SessionAttention(kind: .outgoing, reason: "\(count)\(target) not delivered")
        }
        for (id, items) in Dictionary(grouping: attention.filter { live[$0.sessionId] != nil }, by: \.sessionId) {
            let reasons = Set(items.map(\.reason))
            let reason: String
            if items.count == 1 {
                reason = items[0].reason
            } else if reasons.count == 1 {
                reason = "\(items.count) messages: \(reasons.first!)"
            } else {
                reason = "\(items.count) messages need a decision"
            }
            result[id] = SessionAttention(kind: .incoming, reason: reason)
        }
        for session in live.values {
            if let waiting = session.waitingOn, session.replacedLead?.isEmpty == false {
                result[session.id] = SessionAttention(kind: .replacedRequest, reason: "From a replaced lead. " + requestReason(waiting.kind, place: session.waitingPlace ?? .elsewhere))
            } else if let waiting = session.waitingOn {
                result[session.id] = SessionAttention(kind: .request, reason: requestReason(waiting.kind, place: session.waitingPlace ?? .elsewhere))
            } else if session.pendingAskID != nil {
                let headline = session.endTurn?.headline ?? ""
                result[session.id] = SessionAttention(kind: .request, reason: headline.isEmpty ? requestReason("question") : "Asks: \(headline)")
            } else if let limit = session.limitLabel, result[session.id] == nil {
                result[session.id] = SessionAttention(kind: .usageLimit, reason: limit)
            } else if let stop = session.turnStop, result[session.id] == nil, SessionDisplayKind.of(session) == .stopped {
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
