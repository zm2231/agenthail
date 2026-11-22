import Foundation

struct ClaudeCompactDraft: Equatable {
    var root = ""
    var model = ""
    var window = ""
    var enabled: Bool?
    var idle: Bool?
    var agenthailIdle: Bool?
    var minTokens = ""
    var preLimit = ""

    var problem: String? {
        let tokens = minTokens.trimmingCharacters(in: .whitespaces)
        if !tokens.isEmpty, Int64(tokens).map({ $0 > 0 }) != true { return "From context size must be a whole number of tokens." }
        if preLimitPercent == nil, !preLimit.trimmingCharacters(in: .whitespaces).isEmpty { return "Before a usage limit must be off or a percent from 50 to 97." }
        if !model.trimmingCharacters(in: .whitespaces).isEmpty, window.trimmingCharacters(in: .whitespaces).isEmpty {
            return "A model needs a window: tokens, auto, or unset."
        }
        return nil
    }

    var changes: Bool {
        !window.trimmingCharacters(in: .whitespaces).isEmpty || enabled != nil || idle != nil || agenthailIdle != nil || !minTokens.trimmingCharacters(in: .whitespaces).isEmpty || !preLimit.trimmingCharacters(in: .whitespaces).isEmpty
    }

    var preLimitPercent: Int? {
        let text = preLimit.trimmingCharacters(in: .whitespaces).lowercased()
        if text == "off" { return 0 }
        guard let percent = Int(text.hasSuffix("%") ? String(text.dropLast()) : text), (50...97).contains(percent) else { return nil }
        return percent
    }

    var body: [String: Any] {
        var body: [String: Any] = ["root": root, "model": model.trimmingCharacters(in: .whitespaces), "window": window.trimmingCharacters(in: .whitespaces)]
        if let enabled { body["enabled"] = enabled }
        if let idle { body["idleCompaction"] = idle }
        if let agenthailIdle { body["agenthailIdleCompaction"] = agenthailIdle }
        if let tokens = Int64(minTokens.trimmingCharacters(in: .whitespaces)) { body["agenthailIdleMinTokens"] = tokens }
        if let percent = preLimitPercent { body["agenthailPreLimitPercent"] = percent }
        return body
    }

    mutating func saved() {
        self = ClaudeCompactDraft(root: root)
    }
}
