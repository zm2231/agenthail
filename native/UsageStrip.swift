import Foundation

struct UsageStrip: Equatable {
    struct Other: Equatable, Identifiable {
        let id: String
        let title: String
        let percent: Double
        let critical: Bool
        let spoken: String
    }

    var meters: [UsageReport.Meter] = []
    var others: [Other] = []
    var problem: String?

    static func make(_ sets: [UsageReport.Set], now: Date) -> UsageStrip {
        var strip = UsageStrip()
        var problems: [String] = []
        for set in sets {
            let visible = UsageMeterText.visible(set.meters ?? [], now: now)
            if inUse(set) {
                strip.meters += visible
                if let problem = set.problem, !problem.isEmpty { problems.append(problem) }
            } else if let top = visible.max(by: { $0.percent < $1.percent }) {
                let name = title(set)
                strip.others.append(Other(
                    id: set.hostId + "|" + (set.provider ?? "") + "|" + (set.root ?? ""),
                    title: name,
                    percent: top.percent,
                    critical: visible.contains { $0.severity == "critical" },
                    spoken: fullTitle(set) + ": " + visible.map { UsageMeterText.spokenLabel($0, now: now) }.joined(separator: "; ")
                ))
            }
        }
        if strip.meters.isEmpty { strip.problem = problems.first }
        return strip
    }

    static func merged(_ answers: [[UsageReport.Set]?]) -> [UsageReport.Set] {
        var seen = Swift.Set<String>()
        var sets: [UsageReport.Set] = []
        for (index, answer) in answers.enumerated() {
            for var set in answer ?? [] where seen.insert(set.hostId + "|" + (set.provider ?? "") + "|" + (set.root ?? "")).inserted {
                if index > 0 { set.local = false }
                sets.append(set)
            }
        }
        return sets
    }

    func others(compact: Bool) -> [Other] {
        compact ? others.filter(\.critical) : others
    }

    static func inUse(_ set: UsageReport.Set) -> Bool {
        set.local == true && (set.primary == true || set.provider == "codex")
    }

    static func title(_ set: UsageReport.Set) -> String {
        let name = set.account.map { $0.split(separator: "@").first.map(String.init) ?? $0 } ?? provider(set)
        return set.local == true ? name : name + " · " + set.host
    }

    static func fullTitle(_ set: UsageReport.Set) -> String {
        let name = set.account ?? provider(set)
        return set.local == true ? name : name + " on " + set.host
    }

    private static func provider(_ set: UsageReport.Set) -> String {
        set.provider == "codex" ? "Codex" : "Claude"
    }
}
