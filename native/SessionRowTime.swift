import Foundation

enum SessionRowTime {
    static func working(_ session: SessionState, now: Date) -> String {
        guard let raw = session.activity?.busySince, let since = TimestampParser.date(raw), now.timeIntervalSince(since) >= 60 else { return "now" }
        return SessionActivity.duration(now.timeIntervalSince(since))
    }

    static func idle(_ session: SessionState) -> String? {
        guard let raw = session.lastActive, let date = TimestampParser.date(raw) else { return nil }
        return date.formatted(.relative(presentation: .numeric, unitsStyle: .abbreviated))
    }

    static func status(_ session: SessionState, now: Date) -> String {
        if session.isWorking { return "Working " + working(session, now: now) }
        return ["Idle", idle(session)].compactMap { $0 }.joined(separator: " · ")
    }
}
