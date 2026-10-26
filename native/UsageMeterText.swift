import Foundation

enum UsageMeterText {
    static func visible(_ meters: [UsageReport.Meter], now: Date) -> [UsageReport.Meter] {
        meters.filter { meter in meter.until.flatMap(date).map { now < $0 } ?? true }
    }

    static func detail(_ meter: UsageReport.Meter, now: Date) -> String? {
        if meter.aged, let asOf = date(meter.asOf) {
            return "as of \(ago(now.timeIntervalSince(asOf))) ago"
        }
        return meter.resetsAt.flatMap(date).map { reset(until: $0, now: now) }
    }

    static func shortLabel(_ meter: UsageReport.Meter) -> String {
        switch meter.kind {
        case "session", "5h": return "Session"
        case "weekly", "weekly_all": return "Week"
        case "weekly_scoped": return "Week, model"
        default: return meter.label
        }
    }

    static func read(_ asOf: Date, now: Date) -> String {
        let minutes = Int(now.timeIntervalSince(asOf) / 60)
        if minutes < 1 { return "read just now" }
        if minutes < 60 { return "read \(minutes)m ago" }
        return "read \(ago(now.timeIntervalSince(asOf))) ago"
    }

    static func percent(_ value: Double) -> String {
        "\(Int(value.rounded()))%"
    }

    static func text(_ meter: UsageReport.Meter, now: Date) -> String {
        percent(meter.percent) + (detail(meter, now: now).map { " · " + $0 } ?? "")
    }

    static func spokenLabel(_ meter: UsageReport.Meter, now: Date) -> String {
        "\(meter.label) usage \(Int(meter.percent.rounded())) percent" + (detail(meter, now: now).map { ", " + $0 } ?? "")
    }

    static func reset(until date: Date, now: Date) -> String {
        let seconds = max(Int(date.timeIntervalSince(now)), 0)
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
        TimestampParser.date(value)
    }
}
