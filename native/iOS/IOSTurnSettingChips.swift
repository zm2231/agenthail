import SwiftUI

struct IOSTurnSettingChips: View {
    @ObservedObject var model: AgenthailIOSModel
    let sessionID: String
    let settings: [TurnSetting]
    let showModels: () -> Void

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 6) {
                if let setting = settings.setting(TurnSetting.model) {
                    Button(action: showModels) { Self.chip(setting.valueLabel) }
                        .buttonStyle(.plain)
                        .accessibilityLabel(setting.chipAccessibilityLabel)
                        .accessibilityIdentifier("composer-model-picker")
                }
                if let setting = settings.setting(TurnSetting.effort) {
                    menu(setting, options: setting.options)
                        .accessibilityIdentifier("composer-effort")
                }
                if let setting = settings.setting(TurnSetting.mode) {
                    menu(setting, options: setting.quietOptions)
                        .accessibilityIdentifier("composer-mode")
                }
            }
        }
        .scrollBounceBehavior(.basedOnSize, axes: .horizontal)
        .disabled(model.pendingControls.contains(sessionID))
    }

    private func menu(_ setting: TurnSetting, options: [TurnSetting.Option]) -> some View {
        Menu {
            TurnSettingPicker(model: model, sessionID: sessionID, setting: setting, options: options)
        } label: {
            Self.chip(setting.valueLabel)
        }
        .accessibilityLabel(setting.chipAccessibilityLabel)
    }

    static func chip(_ text: String) -> some View {
        Text(text).font(.caption.weight(.medium))
            .foregroundStyle(.primary)
            .lineLimit(1)
            .fixedSize()
            .padding(.horizontal, 12).frame(minHeight: 44)
            .background(Color.primary.opacity(0.05), in: Capsule())
    }
}
