import Foundation

enum NewSessionLabels {
    static func agent(_ agent: String, model: String?, effort: String, account: String?) -> String {
        [agent.capitalized, account, model, effort.isEmpty ? nil : effort.capitalized].compactMap { $0 }.joined(separator: " · ")
    }

    static func place(_ folder: String, host: String?) -> String {
        [host, folder].compactMap { $0 }.joined(separator: " · ")
    }

    static func account(_ roots: [ClaudeConfigRootOption], choice: String, pick: ClaudeRootPick?) -> String? {
        guard roots.count > 1 else { return nil }
        if choice.isEmpty { return pick.map { "auto: \($0.name)" } }
        return roots.first { $0.root == choice }?.name
    }
}
