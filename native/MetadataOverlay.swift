import Foundation

struct MetadataOverlay {
    private(set) var context: ContextState?
    private(set) var goal: GoalState?
    private(set) var model: String?
    private(set) var models: [ModelOption]?
    private(set) var turnSettings: [TurnSetting]?
    private(set) var compactSettings: [TurnSetting]?
    private(set) var claudeRuns: [ClaudeRunObservation]?
    private(set) var claudeSubagents: [ClaudeSubagentLink]?
    private(set) var claudeSubagentsTotal: Int?
    private(set) var allClaudeSubagents = false
    private(set) var metadataErrors: [String: String]?
    private(set) var settled = false
    private var streamed: Set<String> = []

    init(seed detail: SessionDetail? = nil, allClaudeSubagents: Bool = false) {
        self.allClaudeSubagents = allClaudeSubagents
        context = detail?.context
        goal = detail?.goal
        model = detail?.model
        models = detail?.models
        turnSettings = detail?.turnSettings
        compactSettings = detail?.compactSettings
        claudeRuns = detail?.claudeRuns
        claudeSubagents = detail?.claudeSubagents
        claudeSubagentsTotal = detail?.claudeSubagentsTotal
        metadataErrors = detail?.metadataErrors
    }

    mutating func absorb(_ metadata: SessionMetadata) {
        absorb(context: metadata.context, goal: metadata.goal, model: metadata.model, models: metadata.models, turnSettings: metadata.turnSettings, compactSettings: metadata.compactSettings)
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

    mutating func absorb(context: ContextState?, goal: GoalState?, model: String?, models: [ModelOption]?, turnSettings: [TurnSetting]? = nil, compactSettings: [TurnSetting]? = nil) {
        if !streamed.contains("context"), let context { self.context = context }
        if !streamed.contains("goal"), let goal { self.goal = goal }
        if let model { self.model = model }
        if let models { self.models = models }
        if let turnSettings { self.turnSettings = turnSettings }
        if let compactSettings { self.compactSettings = compactSettings }
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
        merged.turnSettings = turnSettings
        merged.compactSettings = compactSettings
        merged.claudeRuns = claudeRuns
        merged.claudeSubagents = claudeSubagents
        merged.claudeSubagentsTotal = claudeSubagentsTotal
        merged.metadataErrors = metadataErrors
        return merged
    }
}

enum MetadataWarnings {
    static let fieldNames = [
        "context": "Context usage", "goal": "Goal", "model": "Current model", "models": "Model list",
        "turnSettings": "Turn settings", "compactSettings": "Compact settings",
        "claudeRuns": "Claude runs", "claudeSubagents": "Subagents",
    ]

    static func lines(_ errors: [String: String]) -> [String] {
        errors.keys.sorted().map { key in
            let name = fieldNames[key] ?? key
            let reason = errors[key].map { $0.trimmingCharacters(in: .whitespacesAndNewlines) } ?? ""
            return reason.isEmpty ? name : "\(name): \(reason)"
        }
    }
}
