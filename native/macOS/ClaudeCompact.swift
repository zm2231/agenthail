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

    static func modelLines(_ root: ClaudeCompactRoot) -> [String] {
        (root.modelWindows ?? [:]).sorted { $0.key < $1.key }.map { "\($0.key): \(window($0.value))" }
    }

    static func holdLines(_ root: ClaudeCompactRoot, holds: [ClaudeCompactHold], now: Date = Date()) -> [String] {
        holds.filter { $0.root == root.root }.suffix(holdsShown).reversed().map { hold in
            let name = hold.session.flatMap { $0.isEmpty ? nil : $0 } ?? hold.sessionId
            let age = TurnClock.date(hold.at).map { RelativeDateTimeFormatter().localizedString(for: $0, relativeTo: now) } ?? hold.at
            return "Not compacted: \(name) at \(hold.contextTokens.formatted()) tokens, \(age): \(hold.reason)"
        }
    }

    static func overrideLines(_ root: ClaudeCompactRoot) -> [String] {
        (root.overrides ?? [:]).sorted { $0.key < $1.key }.map { "\($0.key)=\($0.value) is set where Agenthail runs and takes precedence over this file." }
    }
}
