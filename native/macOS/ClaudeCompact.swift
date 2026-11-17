import Foundation

enum ClaudeCompactFacts {
    static let holdsShown = 5

    static func window(_ window: ClaudeCompactWindow?) -> String {
        guard let window else { return "Claude's default" }
        if window.auto == true { return "auto" }
        if let ignored = window.ignored, !ignored.isEmpty { return "\(ignored) (Claude ignores it)" }
        return "\((window.tokens ?? 0).formatted()) tokens"
    }

    static func toggle(_ value: Bool?) -> String {
        value.map { $0 ? "on" : "off" } ?? "not set"
    }

    static func agenthailIdle(_ idle: ClaudeIdleCompaction) -> String {
        idle.enabled ? "on, from \(idle.minContextTokens.formatted()) tokens" : "off"
    }

    static func modelLines(_ root: ClaudeCompactRoot) -> [(model: String, window: String)] {
        (root.modelWindows ?? [:]).sorted { $0.key < $1.key }.map { (modelName($0.key), window($0.value)) }
    }

    static func modelName(_ id: String) -> String {
        let parts = id.split(separator: "-").map(String.init)
        guard parts.count >= 3, parts[0] == "claude", let major = Int(parts[2]) else { return id }
        let minor = parts.count > 3 && parts[3].count <= 2 ? Int(parts[3]) : nil
        return parts[1].capitalized + " " + (minor.map { "\(major).\($0)" } ?? String(major))
    }

    static func title(_ root: String, home: String = NSHomeDirectory()) -> String {
        var parts = PathNames.components(root)
        if parts.last == ".claude" { parts.removeLast() }
        if parts == PathNames.components(home) { return "Default" }
        guard let name = parts.last else { return root }
        return name.hasPrefix(".") ? String(name.dropFirst()) : name
    }

    static func holdLines(_ root: ClaudeCompactRoot, holds: [ClaudeCompactHold], now: Date = Date()) -> [String] {
        holds.filter { $0.root == root.root }.suffix(holdsShown).reversed().map { hold in
            let name = hold.session.flatMap { $0.isEmpty ? nil : $0 } ?? hold.sessionId
            let age = TurnClock.date(hold.at).map { RelativeDateTimeFormatter().localizedString(for: $0, relativeTo: now) } ?? hold.at
            return "\(name) at \(hold.contextTokens.formatted()) tokens, \(age): \(hold.reason)"
        }
    }

    static func overrideLines(_ root: ClaudeCompactRoot) -> [String] {
        (root.overrides ?? [:]).sorted { $0.key < $1.key }.map { "\($0.key)=\($0.value) is set where Agenthail runs and takes precedence over this file." }
    }
}
