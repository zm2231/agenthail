import Foundation

struct SurfaceProblem: Equatable {
    enum Fix: Equatable {
        case browserSignIn
        case repair(action: String, label: String)
    }

    let sentence: String
    let fix: Fix?
    let details: [String]

    var fixLabel: String? {
        switch fix {
        case .browserSignIn: return "Open Browser Sign-in"
        case .repair(_, let label): return label
        case nil: return nil
        }
    }
}

struct SurfaceOverview: Identifiable, Equatable {
    enum Health: Equatable {
        case healthy
        case degraded
        case signinOff
        case signinUnavailable
        case agentOff
        case agentUnavailable
        case unavailable
    }

    let surface: SurfaceState
    let health: Health
    let detail: String
    let availability: String?
    let advice: [String]
    let problem: SurfaceProblem?
    let working: Int
    let current: Int
    let currentWindow: String
    let queued: Int

    var id: String { surface.name }
    var isQuiet: Bool { health == .healthy || surface.isStatedLimit }
    var name: String { SessionRefinement.surfaceLabel(surface.name) }

    var stateLabel: String {
        switch health {
        case .healthy: return "Healthy"
        case .degraded: return "Needs attention"
        case .signinOff: return "Sign-in off"
        case .signinUnavailable: return "No browser here"
        case .agentOff: return "Off"
        case .agentUnavailable: return "Not installed here"
        case .unavailable: return "Unavailable"
        }
    }

    static func build(surfaces: [SurfaceState], sessions: [SessionState], codexRecentHours: Int) -> [SurfaceOverview] {
        surfaces.map { surface in
            let owned = sessions.filter { $0.surface == surface.name }
            let current = owned.filter { surface.name == "claude" ? $0.open : $0.currentReason == "recent" }.count
            return SurfaceOverview(
                surface: surface,
                health: health(surface),
                detail: detail(surface),
                availability: surface.runtime.map(availability),
                advice: surface.runtime?.advice ?? [],
                problem: problem(surface),
                working: owned.filter { $0.isWorking && $0.waitingOn == nil }.count,
                current: current,
                currentWindow: surface.name == "claude" ? "Open now" : "Past \(codexRecentHours)h",
                queued: owned.reduce(0) { $0 + $1.queueCount }
            )
        }
    }

    private static func health(_ surface: SurfaceState) -> Health {
        switch surface.health {
        case "healthy": return .healthy
        case "degraded": return .degraded
        case "signin_off": return .signinOff
        case "signin_unavailable": return .signinUnavailable
        case "agent_off": return .agentOff
        case "agent_unavailable": return .agentUnavailable
        default: return .unavailable
        }
    }

    private static func detail(_ surface: SurfaceState) -> String {
        problem(surface)?.sentence ?? [surface.healthDetail, surface.error].compactMap { $0 }.first { !$0.isEmpty } ?? "Ready"
    }

    static func problem(_ surface: SurfaceState) -> SurfaceProblem? {
        let raw = [surface.healthDetail, surface.error].compactMap { $0 }.filter { !$0.isEmpty }
        let text = raw.first ?? ""
        let name = SessionRefinement.surfaceLabel(surface.name)
        var details = raw
        if let runtime = surface.runtime {
            details.append(availability(runtime))
            details += runtime.advice
        }
        details = details.reduce(into: []) { if !$0.contains($1) { $0.append($1) } }
        let consumer = text.range(of: ": ").map { String(text[..<$0.lowerBound]) } ?? name
        switch surface.health {
        case "healthy":
            return nil
        case "signin_off":
            return SurfaceProblem(sentence: "\(consumer) can't sign in: browser sign-in is off.", fix: .browserSignIn, details: details)
        case "signin_unavailable":
            return SurfaceProblem(sentence: "\(consumer) can't sign in: this computer has no browser.", fix: .browserSignIn, details: details)
        case "agent_off":
            return SurfaceProblem(sentence: "\(name) is turned off on this Mac.", fix: nil, details: details)
        case "agent_unavailable":
            return SurfaceProblem(sentence: "\(name) is not installed on this Mac.", fix: nil, details: details)
        default:
            break
        }
        if text.contains("agenthail signin use") {
            return SurfaceProblem(sentence: "\(consumer) can't sign in: \(signinReason(text)).", fix: .browserSignIn, details: details)
        }
        if text.range(of: #"notion.*(401|context)"#, options: [.regularExpression, .caseInsensitive]) != nil {
            return SurfaceProblem(sentence: "Notion is not signed in on this Mac.", fix: .browserSignIn, details: details)
        }
        if text.range(of: "cookie bridge", options: .caseInsensitive) != nil {
            return SurfaceProblem(sentence: "Claude Code needs to reconnect to your signed-in account.", fix: nil, details: details)
        }
        let fix = surface.repairAction.map { SurfaceProblem.Fix.repair(action: $0, label: repairLabel($0, daemonLabel: surface.repairLabel)) }
        switch surface.repairAction {
        case "codex-launch":
            return SurfaceProblem(sentence: "Codex isn't connected: launch it through Agenthail to reach its sessions.", fix: fix, details: details)
        case "runtime-ensure":
            let sentence = text.range(of: "reachable but not supervised", options: .caseInsensitive) != nil
                ? "The Codex background service will stop after a restart."
                : "The Codex background service isn't running."
            return SurfaceProblem(sentence: sentence, fix: fix, details: details)
        default:
            break
        }
        if surface.connected {
            return SurfaceProblem(sentence: "\(name) is connected but reports a problem.", fix: fix, details: details)
        }
        return SurfaceProblem(sentence: "\(name) isn't available right now.", fix: fix, details: details)
    }

    private static func signinReason(_ text: String) -> String {
        let browsers = ["Safari", "Chrome", "Arc", "Brave", "Edge", "Firefox", "Chromium", "Vivaldi", "Orion", "Opera"]
        let browser = browsers.first { text.contains($0) }
        if text.range(of: "no cookie store", options: .caseInsensitive) != nil || text.range(of: "no saved sign-in", options: .caseInsensitive) != nil {
            return "\(browser ?? "the browser") has no saved sign-in"
        }
        if text.range(of: "not permitted", options: .caseInsensitive) != nil || text.range(of: "permission", options: .caseInsensitive) != nil {
            return "Agenthail isn't allowed to read \(browser ?? "the browser")'s sign-in"
        }
        return "\(browser ?? "the browser")'s sign-in can't be read"
    }

    private static func repairLabel(_ action: String, daemonLabel: String?) -> String {
        switch action {
        case "codex-launch": return "Launch Codex"
        case "runtime-ensure": return "Start Codex Service"
        default: return daemonLabel ?? "Fix"
        }
    }

    private static func availability(_ runtime: SurfaceRuntime) -> String {
        guard runtime.reachable else { return "\(runtime.name) is not connected." }
        return runtime.durable ? "\(runtime.name) stays available in the background." : "\(runtime.name) is open now but will not restart automatically."
    }
}

enum OverviewSummary {
    static func line(_ snapshot: DashboardSnapshot, working: Int, needsYou: Int) -> String {
        if snapshot.daemon.stale == true {
            return "Showing cached data. \(snapshot.daemon.refreshError ?? "Agent app refresh is temporarily unavailable.")"
        }
        guard snapshot.daemon.running else { return "Start Agenthail to deliver work." }
        let connected = snapshot.surfaces.filter(\.connected).count
        let queued = snapshot.sessions.reduce(0) { $0 + $1.queueCount }
        var parts = [connected == 0 ? "No agent apps connected" : count(connected, "agent app") + " connected"]
        if working > 0 { parts.append("\(working) working") }
        if needsYou > 0 { parts.append("\(needsYou) need\(needsYou == 1 ? "s" : "") you") }
        if queued > 0 { parts.append(count(queued, "message") + " queued") }
        return parts.joined(separator: " · ")
    }

    private static func count(_ value: Int, _ noun: String) -> String {
        "\(value) \(noun)\(value == 1 ? "" : "s")"
    }
}

struct DeliveryOutcome: Identifiable, Equatable {
    enum Tone: Equatable {
        case done
        case pending
        case problem
    }

    let id: Int64
    let label: String
    let target: String
    let detail: String
    let isError: Bool
    let tone: Tone
    let createdAt: String

    static let limit = 8

    static func recent(_ history: [HistoryState]) -> [DeliveryOutcome] {
        history.compactMap { entry in
            guard let evidence = entry.evidence, !evidence.isEmpty else { return nil }
            let error = entry.error.flatMap { $0.isEmpty ? nil : $0 }
            let detail = error ?? [entry.message, entry.result].compactMap { $0 }.first { !$0.isEmpty } ?? "No details recorded"
            return DeliveryOutcome(id: entry.id, label: label(evidence, kind: entry.kind), target: entry.target.flatMap { $0.isEmpty ? nil : $0 } ?? "Agenthail", detail: detail, isError: error != nil, tone: tone(evidence), createdAt: entry.createdAt)
        }
        .prefix(limit)
        .map { $0 }
    }

    private static func label(_ evidence: String, kind: String) -> String {
        switch evidence {
        case "queued": return "Queued"
        case "transport_accepted": return "Transport accepted"
        case "held": return "Held by the receiver"
        case "delivered": return "Delivered"
        case "reply_observed": return "Reply observed"
        case "failed": return "Failed"
        case "unknown": return "Never confirmed"
        case "canceled": return "Canceled"
        default: return kind.replacingOccurrences(of: "_", with: " ").capitalized
        }
    }

    private static func tone(_ evidence: String) -> Tone {
        switch evidence {
        case "delivered", "reply_observed": return .done
        case "failed", "unknown": return .problem
        default: return .pending
        }
    }
}

enum CodexRecentWindow {
    static let range = 1...168
    static let presets = [1, 3, 5, 8, 12, 24]

    static func choices(including current: Int) -> [Int] {
        range.contains(current) && !presets.contains(current) ? (presets + [current]).sorted() : presets
    }

    static func label(_ hours: Int) -> String {
        hours == 1 ? "1 hour" : "\(hours) hours"
    }
}
