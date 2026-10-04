import Foundation

struct MetadataOverlay {
    private(set) var context: ContextState?
    private(set) var goal: GoalState?
    private(set) var model: String?
    private(set) var models: [ModelOption]?
    private var streamed: Set<String> = []

    init(seed detail: SessionDetail? = nil) {
        context = detail?.context
        goal = detail?.goal
        model = detail?.model
        models = detail?.models
    }

    mutating func absorb(context: ContextState?, goal: GoalState?, model: String?, models: [ModelOption]?) {
        if !streamed.contains("context"), let context { self.context = context }
        if !streamed.contains("goal"), let goal { self.goal = goal }
        if let model { self.model = model }
        if let models { self.models = models }
    }

    mutating func stream(context: ContextState?) {
        streamed.insert("context")
        self.context = context
    }

    mutating func stream(goal: GoalState?) {
        streamed.insert("goal")
        self.goal = goal
    }

    func apply(to detail: SessionDetail) -> SessionDetail {
        var merged = detail
        merged.context = context
        merged.goal = goal
        merged.model = model
        merged.models = models
        return merged
    }
}
