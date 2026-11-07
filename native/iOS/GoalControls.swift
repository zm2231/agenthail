import Foundation

struct GoalControls: Equatable {
    enum Control: Equatable {
        case setGoal
        case editGoal
        case clearGoal
        case pause
        case resume
        case setBudget
        case editBudget
        case clearBudget
    }

    let controls: [Control]

    init(_ detail: SessionDetail) {
        guard !detail.readOnly && detail.capabilities.goalSet else {
            controls = []
            return
        }
        let hasObjective = detail.goal?.objective.isEmpty == false
        var list: [Control] = [hasObjective ? .editGoal : .setGoal]
        if hasObjective { list.append(.clearGoal) }
        if detail.capabilities.goal, let goal = detail.goal {
            if goal.status == "active" {
                list.append(.pause)
            } else if goal.status == "paused" {
                list.append(.resume)
            }
            list.append(goal.tokenBudget == nil ? .setBudget : .editBudget)
            if goal.tokenBudget != nil { list.append(.clearBudget) }
        }
        controls = list
    }

    func offers(_ control: Control) -> Bool { controls.contains(control) }
}
