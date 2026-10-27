import Foundation

enum ClaudeRootLabels {
    struct Usage: Equatable {
        let text: String
        let overLimit: Bool
    }

    static func titles(_ roots: [ClaudeConfigRootOption], host: String) -> [String] {
        let names = roots.map { root -> String in
            if let proxy = root.proxy, !proxy.isEmpty { return "\(proxy) (proxy)" }
            if let email = root.signedInAs?.email, !email.isEmpty { return String(email.split(separator: "@")[0]) }
            return "\(host) (\(root.login == "api_key" ? "API key" : "signed out"))"
        }
        return zip(roots, names).map { root, name in
            names.filter { $0 == name }.count > 1 ? "\(name) · \(root.name)" : name
        }
    }

    static func usage(_ root: ClaudeConfigRootOption, pick: ClaudeRootPick?) -> Usage? {
        let windows: [(String, Set<String>, Double?)] = [
            ("session", ["session", "5h"], pick?.sessionCeiling),
            ("week", ["weekly", "weekly_all"], pick?.weeklyCeiling),
        ]
        var parts: [String] = []
        var over = false
        for (label, kinds, ceiling) in windows {
            guard let limit = root.usage?.limits?.first(where: { kinds.contains($0.kind) }) else { continue }
            parts.append("\(label) \(Int(limit.percent.rounded()))%")
            if let ceiling, limit.percent >= ceiling { over = true }
        }
        if over { parts.append("over limit") }
        return parts.isEmpty ? nil : Usage(text: parts.joined(separator: ", "), overLimit: over)
    }

    static func option(_ root: ClaudeConfigRootOption, title: String, pick: ClaudeRootPick?) -> String {
        if let until = root.exhaustedUntil() {
            return "\(title) · out of usage until \(until.formatted(date: .abbreviated, time: .shortened))"
        }
        return usage(root, pick: pick).map { "\(title) · \($0.text)" } ?? title
    }

    static func picked(_ pick: ClaudeRootPick, roots: [ClaudeConfigRootOption], titles: [String]) -> String {
        let index = roots.firstIndex { pick.root.isEmpty ? $0.primary : $0.root == pick.root }
        return index.map { titles[$0] } ?? pick.name
    }

    static func automatic(_ pick: ClaudeRootPick?, roots: [ClaudeConfigRootOption], titles: [String]) -> String {
        guard let pick else { return "Automatic" }
        let reason = pick.reason.split(whereSeparator: { $0 == "," || $0 == ";" }).first.map(String.init) ?? pick.reason
        return "Automatic (\(picked(pick, roots: roots, titles: titles)), \(reason))"
    }

    static func reason(_ pick: ClaudeRootPick, roots: [ClaudeConfigRootOption], titles: [String]) -> String {
        "Automatic picks \(picked(pick, roots: roots, titles: titles)): \(pick.reason)"
    }
}
