import Foundation

struct SurfaceOverview: Identifiable, Equatable {
    enum Health: Equatable {
        case healthy
        case degraded
        case unavailable
    }

    let surface: SurfaceState
    let health: Health
    let detail: String
    let availability: String?
    let advice: [String]
    let working: Int
    let current: Int
    let currentWindow: String
    let queued: Int

    var id: String { surface.name }
    var name: String { SessionRefinement.surfaceLabel(surface.name) }

    var stateLabel: String {
        switch health {
        case .healthy: return "Healthy"
        case .degraded: return "Needs attention"
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
                working: owned.filter { $0.currentReason == "working" }.count,
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
        default: return .unavailable
        }
    }

    private static func detail(_ surface: SurfaceState) -> String {
        let raw = [surface.healthDetail, surface.error].compactMap { $0 }.first { !$0.isEmpty } ?? "Ready"
        if raw.range(of: "cookie bridge", options: .caseInsensitive) != nil {
            return "Claude Code needs to reconnect to your signed-in account."
        }
        if raw.range(of: #"notion.*(401|context)"#, options: [.regularExpression, .caseInsensitive]) != nil {
            return "Notion is not signed in on this Mac."
        }
        if raw.range(of: "reachable but not supervised", options: .caseInsensitive) != nil {
            return "The Codex background service will stop after a restart."
        }
        return raw
    }

    private static func availability(_ runtime: SurfaceRuntime) -> String {
        guard runtime.reachable else { return "\(runtime.name) is not connected." }
        return runtime.durable ? "\(runtime.name) stays available in the background." : "\(runtime.name) is open now but will not restart automatically."
    }
}

enum OverviewSummary {
    static func line(_ snapshot: DashboardSnapshot) -> String {
        if snapshot.daemon.stale == true {
            return "Showing cached data. \(snapshot.daemon.refreshError ?? "Surface refresh is temporarily unavailable.")"
        }
        guard snapshot.daemon.running else { return "Start Agenthail to deliver work." }
        let connected = snapshot.surfaces.filter(\.connected).count
        let working = snapshot.sessions.filter { $0.currentReason == "working" }.count
        let queued = snapshot.sessions.reduce(0) { $0 + $1.queueCount }
        var parts = [connected == 0 ? "No surfaces connected" : count(connected, "surface") + " connected"]
        if working > 0 { parts.append(count(working, "agent") + " working") }
        if queued > 0 { parts.append(count(queued, "message") + " waiting") }
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
        case "held": return "Held by receiver"
        case "delivered": return "Delivered"
        case "reply_observed": return "Reply observed"
        case "failed": return "Failed"
        case "unknown": return "Delivery uncertain"
        case "expired": return "Expired"
        case "canceled": return "Canceled"
        default: return kind.replacingOccurrences(of: "_", with: " ").capitalized
        }
    }

    private static func tone(_ evidence: String) -> Tone {
        switch evidence {
        case "delivered", "reply_observed": return .done
        case "failed", "unknown", "expired": return .problem
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
