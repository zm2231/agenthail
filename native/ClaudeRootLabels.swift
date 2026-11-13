import Foundation

enum ClaudeRootLabels {
    static func titles(_ roots: [ClaudeConfigRootOption], host: String) -> [String] {
        let names = roots.map { root -> String in
            if let proxy = root.proxy, !proxy.isEmpty { return "\(proxy) via proxy" }
            if let email = root.signedInAs?.email, !email.isEmpty { return String(email.split(separator: "@")[0]) }
            return "\(host) (\(UsageAccounts.loginLabel(root.login, source: root.loginSource) ?? "signed out"))"
        }
        return zip(roots, names).map { root, name in
            names.filter { $0 == name }.count > 1 ? "\(name) · \(root.name)" : name
        }
    }

    static func usage(_ root: ClaudeConfigRootOption) -> String? {
        let windows: [(String, Set<String>)] = [("session", ["session", "5h"]), ("week", ["weekly", "weekly_all"])]
        let parts = windows.compactMap { label, kinds in
            root.usage?.limits?.first(where: { kinds.contains($0.kind) }).map { "\(label) \(Int($0.percent.rounded()))%" }
        }
        return parts.isEmpty ? nil : parts.joined(separator: ", ")
    }

    static func option(_ root: ClaudeConfigRootOption, title: String) -> String {
        if let until = root.exhaustedUntil() {
            return "\(title) · out of usage until \(until.formatted(date: .abbreviated, time: .shortened))"
        }
        let best = root.best.map { "next: " + ($0.split(separator: "@").first.map(String.init) ?? $0) }
        return [title, best, usage(root)].compactMap { $0 }.joined(separator: " · ")
    }

    static func picked(_ pick: ClaudeRootPick, roots: [ClaudeConfigRootOption], titles: [String]) -> String {
        let index = roots.firstIndex { pick.root.isEmpty ? $0.primary : $0.root == pick.root }
        return index.map { titles[$0] } ?? pick.name
    }

    static func byDefault(_ pick: ClaudeRootPick?, roots: [ClaudeConfigRootOption], titles: [String]) -> String {
        guard let pick else { return "Default" }
        let reason = pick.reason.split(whereSeparator: { $0 == "," || $0 == ";" }).first.map(String.init) ?? pick.reason
        return "Default (\(picked(pick, roots: roots, titles: titles)), \(reason))"
    }

    static func reason(_ pick: ClaudeRootPick, roots: [ClaudeConfigRootOption], titles: [String]) -> String {
        "Default is \(picked(pick, roots: roots, titles: titles)): \(pick.reason)"
    }
}
