import Foundation

enum InspectorSettingText {
    static func title(_ setting: TurnSetting) -> String {
        switch setting.key {
        case TurnSetting.compactWindow: "Compact new sessions at"
        case TurnSetting.sessionCompactWindow: "Compact this session at"
        default: setting.chipName
        }
    }

    static func label(_ option: TurnSetting.Option, in setting: TurnSetting) -> String {
        setting.key == TurnSetting.sessionCompactWindow && option.id == "default" ? "Same as new sessions" : option.label
    }

    static func value(_ setting: TurnSetting) -> String {
        setting.current.map { label($0, in: setting) } ?? setting.valueLabel
    }

    static func note(_ setting: TurnSetting) -> String? {
        if setting.pending != nil { return setting.appliesNote }
        switch setting.applies {
        case "newSessions": return nil
        case "nextMessage": return "Applies when the session next goes idle"
        default: return setting.appliesNote
        }
    }
}
