import Foundation

enum NewSessionLabels {
    static func agent(_ agent: String, model: String?, effort: String, account: String?) -> String {
        [agent.capitalized, account, model, effort.isEmpty ? nil : effort.capitalized].compactMap { $0 }.joined(separator: " · ")
    }

    static func options(_ form: SessionLaunchForm) -> String {
        let set = form.setsLaunchOptions ? [form.approvalPolicy, form.mode, form.serviceTier, form.outputSchema.trimmingCharacters(in: .whitespacesAndNewlines), form.claudeAgent.trimmingCharacters(in: .whitespaces), form.worktree.trimmingCharacters(in: .whitespaces), form.permissionMode].filter { !$0.isEmpty }.count : 0
        let name = form.trimmedAlias.isEmpty ? nil : "@\(form.trimmedAlias)"
        let count = set == 0 ? nil : set == 1 ? "1 option" : "\(set) options"
        let label = [name, count].compactMap { $0 }.joined(separator: " · ")
        return label.isEmpty ? "Options" : label
    }

    static func launcherDetail(_ launcher: LauncherOption) -> String? {
        let detail = launcher.detail?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return detail.isEmpty ? nil : detail
    }

    static func launcherHelp(_ launcher: LauncherOption) -> String {
        launcherDetail(launcher).map { "Runs in \(launcher.label): \($0)" } ?? "Where the agent runs"
    }

    static func place(_ folder: String, host: String?) -> String {
        [host, folder].compactMap { $0 }.joined(separator: " · ")
    }

    static func account(_ roots: [ClaudeConfigRootOption], choice: String, pick: ClaudeRootPick?, host: String) -> String? {
        guard roots.count > 1 else { return nil }
        let titles = ClaudeRootLabels.titles(roots, host: host)
        if choice.isEmpty { return pick.map { "\(ClaudeRootLabels.picked($0, roots: roots, titles: titles)) (default)" } }
        return roots.firstIndex { $0.root == choice }.map { titles[$0] }
    }
}
