import Foundation

struct UsageMeter: Equatable {
    let label: String
    let percent: Double
    let detail: String?

    var text: String { "\(Int(percent.rounded()))%" + (detail.map { " · " + $0 } ?? "") }
    var spokenLabel: String { "\(label) usage \(Int(percent.rounded())) percent" + (detail.map { ", " + $0 } ?? "") }
}

enum UsageMeters {
    /// Rollouts update only while Codex runs, so a reading older than this
    /// shows its age instead of its reset.
    static let liveFor: TimeInterval = 3600

    static func meters(provider: String, usage: ProviderUsage?, now: Date) -> [UsageMeter] {
        let fetched = usage?.fetchedAt.flatMap(date)
        let age = fetched.map { now.timeIntervalSince($0) } ?? 0
        return (usage?.limits ?? []).compactMap { limit in
            if limit.kind == "weekly_scoped" && limit.percent <= 0 { return nil }
            let resets = limit.resetsAt.flatMap(date)
            if let resets, resets <= now { return nil }
            if let window = windowSeconds(limit.kind), age > window { return nil }
            let detail = age > liveFor ? "as of \(ago(age)) ago" : resets.map { reset(until: $0, now: now) }
            return UsageMeter(label: label(provider: provider, kind: limit.kind), percent: limit.percent, detail: detail)
        }
    }

    static func label(provider: String, kind: String) -> String {
        switch (provider, kind) {
        case ("Claude", "session"): "Claude"
        case (_, "weekly_all"), (_, "weekly"): provider + " week"
        case (_, "weekly_scoped"): provider + " week, model"
        default: provider + " " + kind.replacingOccurrences(of: "_", with: " ")
        }
    }

    static func windowSeconds(_ kind: String) -> TimeInterval? {
        switch kind {
        case "session", "5h": return 5 * 3600
        case "weekly", "weekly_all", "weekly_scoped": return 7 * 86_400
        default:
            guard kind.hasSuffix("m"), let minutes = Double(kind.dropLast()) else { return nil }
            return minutes * 60
        }
    }

    static func reset(until date: Date, now: Date) -> String {
        let seconds = Int(date.timeIntervalSince(now))
        let days = seconds / 86_400, hours = seconds % 86_400 / 3600, minutes = seconds % 3600 / 60
        if days > 0 { return "\(days)d \(hours)h" }
        if hours > 0 { return "resets \(hours)h \(minutes)m" }
        return "resets \(minutes)m"
    }

    static func ago(_ seconds: TimeInterval) -> String {
        let hours = Int(seconds / 3600)
        return hours >= 24 ? "\(hours / 24)d" : "\(max(hours, 1))h"
    }

    static func date(_ value: String) -> Date? {
        let fractional = ISO8601DateFormatter()
        fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return fractional.date(from: value) ?? ISO8601DateFormatter().date(from: value)
    }
}
