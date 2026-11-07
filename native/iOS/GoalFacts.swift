import Foundation

struct GoalFacts: Equatable {
    struct Row: Equatable {
        let label: String
        let value: String
    }

    let objective: String?
    let status: String
    let needsAttention: Bool
    let rows: [Row]
    let reason: String?

    init(_ goal: GoalState) {
        objective = goal.objective.isEmpty ? nil : goal.objective
        status = goal.displayStatus
        needsAttention = goal.needsAttention
        var rows: [Row] = []
        if let value = goal.timeUsedSeconds { rows.append(Row(label: "Elapsed", value: formatGoalDuration(value))) }
        if let value = goal.tokensUsed { rows.append(Row(label: "Tokens used", value: value.formatted())) }
        if let value = goal.tokenBudget { rows.append(Row(label: "Token budget", value: value.formatted())) }
        if let value = goal.createdAt { rows.append(Row(label: "Created", value: value)) }
        if let value = goal.updatedAt { rows.append(Row(label: "Updated", value: value)) }
        self.rows = rows
        reason = goal.reason
    }
}
