import Foundation

enum SessionRowNote {
    static func text(_ session: SessionState, now: Date = Date()) -> String? {
        if let move = session.move { return move.label }
        switch SessionDisplayKind.of(session, now: now) {
        case .unavailable:
            return session.unavailableReason.flatMap { $0.isEmpty ? nil : $0 }
        case .waiting:
            let label = SessionDisplayKind.label(session, now: now)
            return label == SessionDisplayKind.waiting.label ? nil : label
        case .working:
            return session.activity?.note(now: now)
        case .limited:
            return session.limitLabel ?? SessionDisplayKind.limited.label
        case .stopped:
            return session.turnStop.map { "Stopped: \($0.detail)" }
        case .finishing:
            return SessionDisplayKind.label(session, now: now)
        case .queued, .offline, .idle:
            return nil
        }
    }
}
