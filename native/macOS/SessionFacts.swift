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

    static func presenceLabel(_ session: SessionState, now: Date = Date()) -> String {
        let kind = SessionDisplayKind.of(session, now: now)
        guard kind == .idle || kind == .queued else { return SessionDisplayKind.label(session, now: now) }
        switch session.currentReason {
        case "working": return "Working"
        case "queued": return "Queued"
        case "open": return "Open"
        case "recent": return "Recent"
        case "lead": return "Lead"
        default: return statusLabel(session.status)
        }
    }

    static func queuedLabel(_ count: Int) -> String {
        "\(count) queued"
    }

    static func holdersLabel(_ count: Int) -> String {
        "\(count) terminals"
    }

    static func holdersHelp(_ holders: [SessionHolder]) -> String {
        "Open in \(holders.count) terminals: " + holders.map(\.label).joined(separator: "; ")
    }

    static func rowDetail(_ session: SessionState) -> String {
        var parts = [SessionRefinement.surfaceLabel(session.surface), presenceLabel(session)]
        if let host = session.remoteHostName {
            parts[1] = "\(parts[1]) on \(host)"
        }
        if let plan = session.livePlan { parts.append(plan.detail) }
        if session.queueCount > 0 { parts.append(queuedLabel(session.queueCount)) }
        if !session.sharedHolders.isEmpty { parts.append(holdersLabel(session.sharedHolders.count)) }
        return parts.joined(separator: " · ")
    }

    static func headerFacts(_ session: SessionState) -> [String] {
        session.queueCount > 0 ? [queuedLabel(session.queueCount)] : []
    }

    static func loadFailure(_ error: String) -> String {
        let trimmed = error.trimmingCharacters(in: .whitespacesAndNewlines).trimmingCharacters(in: CharacterSet(charactersIn: "."))
        let reason: String
        if trimmed.isEmpty {
            reason = "Agenthail couldn't read it"
        } else if trimmed.localizedCaseInsensitiveContains("not found") {
            reason = "Agenthail hasn't found its history on this computer"
        } else {
            reason = trimmed.prefix(1).uppercased() + trimmed.dropFirst()
        }
        return reason + ". Trying again."
    }

    static func headerDetail(_ session: SessionState) -> String? {
        let trimmed = session.name.trimmingCharacters(in: .whitespacesAndNewlines)
        let named = trimmed.isEmpty || trimmed == session.id || UUID(uuidString: trimmed) != nil ? nil : SessionNames.display(trimmed)
        let name = session.subagent == nil && session.alias?.isEmpty == false && named != session.alias ? named : nil
        let parts = [name, session.project?.displayName].compactMap { $0?.isEmpty == false ? $0 : nil }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
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
