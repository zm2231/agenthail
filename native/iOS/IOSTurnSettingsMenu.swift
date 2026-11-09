import SwiftUI

struct IOSTurnSettingsMenu: View {
    @ObservedObject var model: AgenthailIOSModel
    let sessionID: String
    let settings: [TurnSetting]
    let showModels: () -> Void

    static func summary(_ settings: [TurnSetting]) -> String {
        [TurnSetting.model, TurnSetting.effort].compactMap { settings.setting($0)?.valueLabel }.joined(separator: " · ")
    }

    var body: some View {
        Menu {
            if let setting = settings.setting(TurnSetting.model) {
                Button("\(setting.chipName): \(setting.valueLabel)", systemImage: "cpu", action: showModels)
            }
            if let setting = settings.setting(TurnSetting.effort) {
                TurnSettingPicker(model: model, sessionID: sessionID, setting: setting, options: setting.options)
            }
            if let setting = settings.setting(TurnSetting.mode) {
                TurnSettingPicker(model: model, sessionID: sessionID, setting: setting, options: setting.quietOptions)
            }
        } label: {
            IOSTurnSettingChips.chip(Self.summary(settings))
        }
        .accessibilityLabel([TurnSetting.model, TurnSetting.effort, TurnSetting.mode].compactMap { settings.setting($0)?.chipAccessibilityLabel }.joined(separator: ", "))
        .accessibilityIdentifier("composer-turn-settings")
        .disabled(model.pendingControls.contains(sessionID))
    }
}
