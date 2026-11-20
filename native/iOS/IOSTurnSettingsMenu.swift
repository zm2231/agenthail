import SwiftUI

struct IOSTurnSettingsMenu: View {
    var model: AgenthailIOSModel
    let sessionID: String
    let settings: [TurnSetting]

    var body: some View {
        if let mode = settings.setting(TurnSetting.mode) {
            Menu {
                TurnSettingPicker(model: model, sessionID: sessionID, setting: mode, options: mode.quietOptions)
            } label: {
                IOSTurnSettingChips.chip(mode.valueLabel)
            }
            .accessibilityLabel(mode.chipAccessibilityLabel)
            .accessibilityIdentifier("composer-turn-settings")
            .disabled(model.pendingControls.contains(sessionID))
        }
    }
}
