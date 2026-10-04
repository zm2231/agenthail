import Foundation

struct MetadataOverlay {
    private(set) var context: ContextState?
    private(set) var goal: GoalState?
    private(set) var model: String?
    private(set) var models: [ModelOption]?
    private(set) var claudeRuns: [ClaudeRunObservation]?
    private(set) var claudeSubagents: [ClaudeSubagentLink]?
    private(set) var metadataErrors: [String: String]?
    private var streamed: Set<String> = []

    init(seed detail: SessionDetail? = nil) {
        context = detail?.context
        goal = detail?.goal
        model = detail?.model
        models = detail?.models
        claudeRuns = detail?.claudeRuns
        claudeSubagents = detail?.claudeSubagents
        metadataErrors = detail?.metadataErrors
    }

    mutating func absorb(_ metadata: SessionMetadata) {
        absorb(context: metadata.context, goal: metadata.goal, model: metadata.model, models: metadata.models)
        claudeRuns = metadata.claudeRuns
        claudeSubagents = metadata.claudeSubagents
        metadataErrors = metadata.errors
    }

    mutating func metadataFailed() {
        claudeRuns = nil
        claudeSubagents = nil
        metadataErrors = nil
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
        merged.claudeRuns = claudeRuns
        merged.claudeSubagents = claudeSubagents
        merged.metadataErrors = metadataErrors
        return merged
    }
}
