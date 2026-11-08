import Foundation

struct TurnSetting: Decodable, Equatable, Sendable {
    struct Option: Decodable, Equatable, Sendable {
        let id: String
        let label: String
        var confirm: Bool? = nil

        var needsConfirmation: Bool { confirm == true }
    }

    static let model = "model"
    static let effort = "effort"
    static let mode = "mode"
    static let autoCompact = "autoCompact"
    static let compactWindow = "compactWindow"
    static let sessionCompactWindow = "sessionCompactWindow"

    let key: String
    let value: String
    let options: [Option]
    let applies: String
    var scope: String? = nil
    var pending: String? = nil

    var appliesNextTurn: Bool { applies == "nextTurn" }
    var appliesNote: String? {
        if let pending {
            return "\(options.first { $0.id == pending }?.label ?? pending) from the next message"
        }
        switch applies {
        case "newSessions": return scope.map { "For new sessions on \($0)" } ?? "For new sessions"
        case "nextTurn": return "From the next turn"
        case "nextMessage": return "Restarts the session when idle"
        case "nextLoad": return "Codex applies it the next time it loads this thread with no other app attached"
        default: return nil
        }
    }
    var current: Option? { options.first { $0.id == value } }
    var quietOptions: [Option] { options.filter { !$0.needsConfirmation } }

    var valueLabel: String {
        if let current { return current.label }
        if !value.isEmpty { return value }
        switch key {
        case Self.effort: return "Effort"
        case Self.mode: return "Default mode"
        default: return "Default"
        }
    }

    var chipName: String {
        switch key {
        case Self.autoCompact: "Auto-compact"
        case Self.compactWindow: "Compact at"
        case Self.sessionCompactWindow: "This session"
        default: key.prefix(1).uppercased() + key.dropFirst()
        }
    }
    var chipAccessibilityLabel: String { "\(chipName), \(valueLabel)" }

    enum Pick: Equatable { case unchanged, apply, confirm }

    func pick(_ option: Option) -> Pick {
        option.id == value ? .unchanged : option.needsConfirmation ? .confirm : .apply
    }

    // Values that let the agent act without asking are only ever chosen from a menu.
    var cycled: Option? {
        let quiet = quietOptions
        guard !quiet.isEmpty else { return nil }
        guard let index = quiet.firstIndex(where: { $0.id == value }) else { return quiet[0] }
        return quiet[(index + 1) % quiet.count]
    }
}

extension Array where Element == TurnSetting {
    func setting(_ key: String) -> TurnSetting? { first { $0.key == key } }

    func modelOptions(catalog: [ModelOption]) -> [ModelOption]? {
        guard let setting = setting(TurnSetting.model) else { return nil }
        let described = setting.options.map { option in
            catalog.first { $0.id == option.id } ?? ModelOption(
                id: option.id, displayName: option.label, description: nil, default: nil,
                allowsCustom: nil, supportedReasoningEfforts: nil, defaultReasoningEffort: nil
            )
        }
        let ids = Set(described.map(\.id))
        return described + catalog.filter { !ids.contains($0.id) }
    }
}

extension SessionDetail {
    func modelOptions(catalog: [ModelOption]? = nil) -> [ModelOption] {
        let known = catalog ?? models ?? []
        return turnSettings?.modelOptions(catalog: known) ?? known
    }

    func menuOptions(_ setting: TurnSetting) -> [TurnSetting.Option] {
        guard setting.key == TurnSetting.model else { return setting.options }
        return modelOptions().map { TurnSetting.Option(id: $0.id, label: $0.displayName) }
    }

    var selectedModelID: String? { turnSettings?.setting(TurnSetting.model)?.value ?? model }
}
