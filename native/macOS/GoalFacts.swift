import Foundation

enum GoalFacts {
    static func attention(_ goal: GoalState?) -> String? {
        guard let goal, goal.needsAttention else { return nil }
        let cause = ["blocked": "Blocked", "usageLimited": "Usage limited", "budgetLimited": "Budget limited"][goal.status] ?? goal.status
        return "Goal needs you: \(cause). Review it in the inspector."
    }

    static func usage(_ goal: GoalState) -> String {
        var parts: [String] = []
        if let seconds = goal.timeUsedSeconds { parts.append("\(duration(seconds)) elapsed") }
        if let tokens = goal.tokensUsed {
            if let budget = goal.tokenBudget {
                parts.append("\(tokens.formatted(.number.notation(.compactName))) of \(budget.formatted(.number.notation(.compactName))) tokens")
            } else {
                parts.append("\(tokens.formatted(.number.notation(.compactName))) tokens")
            }
        } else if let budget = goal.tokenBudget {
            parts.append("\(budget.formatted(.number.notation(.compactName))) token budget")
        }
        return parts.joined(separator: " · ")
    }

    static func times(_ goal: GoalState) -> String {
        [TurnClock.time(goal.createdAt).map { "Created \($0)" }, TurnClock.time(goal.updatedAt).map { "updated \($0)" }]
            .compactMap { $0 }
            .joined(separator: ", ")
    }

    static func duration(_ seconds: Int) -> String {
        if seconds < 60 { return "\(seconds)s" }
        let minutes = seconds / 60
        if minutes < 60 { return "\(minutes)m \(seconds % 60)s" }
        return "\(minutes / 60)h \(minutes % 60)m"
    }
}
