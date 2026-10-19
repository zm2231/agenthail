import Foundation

enum SessionFacts {
    static func statusLabel(_ status: String) -> String {
        switch status {
        case "busy": return "Working"
        case "idle": return "Ready"
        case "offline": return "Offline"
        case "unknown", "notLoaded": return "Unavailable"
        default: return status
        }
    }

    static func presenceLabel(_ session: SessionState) -> String {
        if session.isWaitingOnYou { return "Waiting on you" }
        switch session.currentReason {
        case "working": return "Working"
        case "queued": return "Queued"
        case "open": return "Open"
        case "recent": return "Recent"
        default: return statusLabel(session.status)
        }
    }

    static func queuedLabel(_ count: Int) -> String {
        "\(count) queued"
    }

    static func rowDetail(_ session: SessionState) -> String {
        var parts = [SessionRefinement.surfaceLabel(session.surface), presenceLabel(session)]
        if let host = session.remoteHostName {
            parts[1] = session.isUnavailable ? "Unavailable on \(host)" : "\(parts[1]) on \(host)"
        }
        if session.queueCount > 0 { parts.append(queuedLabel(session.queueCount)) }
        return parts.joined(separator: " · ")
    }

    static func headerFacts(_ session: SessionState, age: String) -> [String] {
        var parts = [queuedLabel(session.queueCount)]
        if session.status != "busy" { parts.append(lastActive(age)) }
        return parts
    }

    static func lastActive(_ age: String) -> String {
        switch age {
        case "": return "last active recently"
        case "now": return "last active just now"
        default: return "last active \(age) ago"
        }
    }

    static func exchangeLabel(_ count: Int) -> String {
        "\(count) recent exchange\(count == 1 ? "" : "s")"
    }

    static func filterSummary(_ filter: SessionFilter, codexRecentHours: Int) -> String {
        switch filter {
        case .running: return "Sessions working now"
        case .recent: return "Working sessions, open Claude sessions, queued work, Codex sessions from the last \(codexRecentHours) hours, and anything active in the last 24 hours"
        case .all: return "Every session that is not archived"
        case .archived: return "Archived sessions and projects. New activity brings them back"
        }
    }
}

enum SettingsCounts {
    static func history(_ count: Int) -> String {
        count == 50 ? "50+" : "\(count)"
    }
}
