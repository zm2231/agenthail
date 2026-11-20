import SwiftUI

struct IOSTurnSettingChips: View {
    var model: AgenthailIOSModel
    let sessionID: String
    let settings: [TurnSetting]

    var body: some View {
        if let mode = settings.setting(TurnSetting.mode) {
            ViewThatFits(in: .horizontal) {
                menu(mode) { Self.chip(mode.valueLabel) }
                    .accessibilityLabel(mode.chipAccessibilityLabel)
                    .accessibilityIdentifier("composer-mode")
                menu(mode) { Self.chip(Image(systemName: "ellipsis")) }
                    .accessibilityLabel(mode.chipAccessibilityLabel)
                    .accessibilityIdentifier("composer-turn-more")
            }
            .disabled(model.pendingControls.contains(sessionID))
        }
    }

    private func menu(_ setting: TurnSetting, label: () -> some View) -> some View {
        Menu {
            TurnSettingPicker(model: model, sessionID: sessionID, setting: setting, options: setting.quietOptions)
        } label: {
            label()
        }
    }

    static func chip(_ text: String) -> some View {
        chip(Text(text))
    }

    static func chip(_ label: some View) -> some View {
        label.font(.caption.weight(.medium))
            .foregroundStyle(.primary)
            .lineLimit(1)
            .fixedSize()
            .padding(.horizontal, 12).frame(minHeight: 44)
            .background(Color.primary.opacity(0.05), in: Capsule())
    }
}
