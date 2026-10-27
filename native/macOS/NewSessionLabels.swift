import Foundation

enum NewSessionLabels {
    static func agent(_ agent: String, model: String?, effort: String, account: String?) -> String {
        [agent.capitalized, account, model, effort.isEmpty ? nil : effort.capitalized].compactMap { $0 }.joined(separator: " · ")
    }

    static func place(_ folder: String, host: String?) -> String {
        [host, folder].compactMap { $0 }.joined(separator: " · ")
    }

    static func account(_ roots: [ClaudeConfigRootOption], choice: String, pick: ClaudeRootPick?, host: String) -> String? {
        guard roots.count > 1 else { return nil }
        let titles = ClaudeRootLabels.titles(roots, host: host)
        if choice.isEmpty { return pick.map { "\(ClaudeRootLabels.picked($0, roots: roots, titles: titles)) (auto)" } }
        return roots.firstIndex { $0.root == choice }.map { titles[$0] }
    }
}
