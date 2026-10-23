import Foundation

struct MetadataOverlay {
    private(set) var context: ContextState?
    private(set) var goal: GoalState?
    private(set) var model: String?
    private(set) var pendingModel: String?
    private(set) var models: [ModelOption]?
    private(set) var claudeRuns: [ClaudeRunObservation]?
    private(set) var claudeSubagents: [ClaudeSubagentLink]?
    private(set) var claudeSubagentsTotal: Int?
    private(set) var allClaudeSubagents = false
    private(set) var metadataErrors: [String: String]?
    private(set) var settled = false
    private var streamed: Set<String> = []

    init(seed detail: SessionDetail? = nil) {
        context = detail?.context
        goal = detail?.goal
        model = detail?.model
        pendingModel = detail?.pendingModel
        models = detail?.models
        claudeRuns = detail?.claudeRuns
        claudeSubagents = detail?.claudeSubagents
        claudeSubagentsTotal = detail?.claudeSubagentsTotal
        metadataErrors = detail?.metadataErrors
    }

    mutating func absorb(_ metadata: SessionMetadata) {
        absorb(context: metadata.context, goal: metadata.goal, model: metadata.model, models: metadata.models)
        pendingModel = metadata.pendingModel
        claudeRuns = metadata.claudeRuns
        claudeSubagents = metadata.claudeSubagents
        claudeSubagentsTotal = metadata.claudeSubagentsTotal
        metadataErrors = metadata.errors
        settled = true
    }

    mutating func showAllClaudeSubagents() {
        allClaudeSubagents = true
    }

    mutating func metadataRequested() {
        settled = false
    }

    var needsModelCatalog: Bool { settled && models?.isEmpty ?? true }

    mutating func metadataFailed() {
        claudeRuns = nil
        claudeSubagents = nil
        claudeSubagentsTotal = nil
        metadataErrors = nil
        settled = true
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
        merged.pendingModel = pendingModel
        merged.models = models
        merged.claudeRuns = claudeRuns
        merged.claudeSubagents = claudeSubagents
        merged.claudeSubagentsTotal = claudeSubagentsTotal
        merged.metadataErrors = metadataErrors
        return merged
    }
}
