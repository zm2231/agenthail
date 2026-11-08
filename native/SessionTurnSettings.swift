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

    let key: String
    let value: String
    let options: [Option]
    let applies: String

    var appliesNextTurn: Bool { applies == "nextTurn" }
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
        let custom: Bool? = catalog.contains { $0.allowsCustom == true } ? true : nil
        let described = setting.options.map { option in
            catalog.first { $0.id == option.id } ?? ModelOption(
                id: option.id, displayName: option.label, description: nil, default: nil,
                allowsCustom: custom, supportedReasoningEfforts: nil, defaultReasoningEffort: nil
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

    var selectedModelID: String? { turnSettings?.setting(TurnSetting.model)?.value ?? model }
}
