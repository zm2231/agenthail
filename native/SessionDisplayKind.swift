import Foundation

// Mirrors the daemon's sessionDisplayState; scripts/fixtures/session-display-vectors.json holds both to it.
enum SessionDisplayKind: String, CaseIterable, Identifiable {
    case unavailable, waiting, working, limited, finishing, queued, offline, idle

    var id: String { rawValue }

    static let downHostStates: Set<String> = ["unreachable", "unauthorized", "incompatible"]

    var label: String {
        switch self {
        case .unavailable: "Unavailable"
        case .waiting: "Waiting on you"
        case .working: "Working"
        case .limited: "Usage limit"
        case .finishing: "Finishing"
        case .queued: "Queued"
        case .offline: "Offline"
        case .idle: "Idle"
        }
    }

    var symbol: String {
        switch self {
        case .unavailable: "exclamationmark.triangle"
        case .waiting: "questionmark.bubble"
        case .working: "waveform"
        case .limited: "gauge.with.dots.needle.100percent"
        case .finishing: "hourglass"
        case .queued: "tray.and.arrow.down"
        case .offline, .idle: "circle"
        }
    }

    static func label(_ session: SessionState, now: Date = Date(), waitingPlace: WaitingPlace? = nil, limit: String? = nil) -> String {
        switch of(session, now: now) {
        case .unavailable: "Unavailable"
        case .waiting: (waitingPlace ?? session.waitingPlace)?.label ?? "Waiting on you"
        case .working: session.workingLabel(now: now)
        case .limited: limit ?? session.limitLabel ?? "Usage limit"
        case .finishing: "Waiting on background work"
        case .queued: "Queued"
        case .offline: "Offline"
        case .idle: session.open ? "Idle" : "Closed"
        }
    }

    static func of(_ session: SessionState, now: Date = Date()) -> SessionDisplayKind {
        if downHostStates.contains(session.host?.state ?? "") || ["unknown", "notLoaded"].contains(session.status) {
            return .unavailable
        }
        if session.waitingOn != nil { return .waiting }
        if session.status == "busy" { return .working }
        if let resume = session.limitResume, resume.state != "released" { return .limited }
        if let limit = session.usageLimit, !lifted(limit, now: now) { return .limited }
        if session.backgroundTasks?.running == true { return .finishing }
        if session.queueCount > 0 { return .queued }
        if session.status == "offline" { return .offline }
        return .idle
    }

    private static func lifted(_ limit: UsageLimitState, now: Date) -> Bool {
        guard let resetsAt = limit.resetsAt, let date = TimestampParser.date(resetsAt) else { return false }
        return now >= date
    }
}
