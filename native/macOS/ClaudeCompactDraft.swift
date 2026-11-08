import Foundation

struct ClaudeCompactDraft: Equatable {
    var root = ""
    var model = ""
    var window = ""
    var enabled: Bool?
    var idle: Bool?
    var agenthailIdle: Bool?
    var minTokens = ""

    var problem: String? {
        let tokens = minTokens.trimmingCharacters(in: .whitespaces)
        if !tokens.isEmpty, Int64(tokens).map({ $0 > 0 }) != true { return "From context size must be a whole number of tokens." }
        if !model.trimmingCharacters(in: .whitespaces).isEmpty, window.trimmingCharacters(in: .whitespaces).isEmpty {
            return "A model needs a window: tokens, auto, or unset."
        }
        return nil
    }

    var changes: Bool {
        !window.trimmingCharacters(in: .whitespaces).isEmpty || enabled != nil || idle != nil || agenthailIdle != nil || !minTokens.trimmingCharacters(in: .whitespaces).isEmpty
    }

    var body: [String: Any] {
        var body: [String: Any] = ["root": root, "model": model.trimmingCharacters(in: .whitespaces), "window": window.trimmingCharacters(in: .whitespaces)]
        if let enabled { body["enabled"] = enabled }
        if let idle { body["idleCompaction"] = idle }
        if let agenthailIdle { body["agenthailIdleCompaction"] = agenthailIdle }
        if let tokens = Int64(minTokens.trimmingCharacters(in: .whitespaces)) { body["agenthailIdleMinTokens"] = tokens }
        return body
    }

    mutating func saved() {
        self = ClaudeCompactDraft(root: root)
    }
}
