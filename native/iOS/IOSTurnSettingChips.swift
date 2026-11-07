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
                    Button(action: showModels) { chip(setting.valueLabel) }
                        .buttonStyle(.plain)
                        .accessibilityLabel("Change model, " + setting.valueLabel)
                        .accessibilityIdentifier("composer-model-picker")
                }
                if let setting = settings.setting(TurnSetting.effort) {
                    menu(setting, options: setting.options, name: "Effort")
                        .accessibilityIdentifier("composer-effort")
                }
                if let setting = settings.setting(TurnSetting.mode) {
                    menu(setting, options: setting.quietOptions, name: "Mode")
                        .accessibilityIdentifier("composer-mode")
                }
            }
        }
        .scrollBounceBehavior(.basedOnSize, axes: .horizontal)
        .disabled(model.pendingControls.contains(sessionID))
    }

    private func menu(_ setting: TurnSetting, options: [TurnSetting.Option], name: String) -> some View {
        Menu {
            Section(setting.appliesNextTurn ? "From the next turn" : "") {
                ForEach(options, id: \.id) { option in
                    Button {
                        guard setting.pick(option) == .apply else { return }
                        Task { await model.setTurnSetting(setting.key, to: option.id, sessionID: sessionID) }
                    } label: {
                        if option.id == setting.value { Label(option.label, systemImage: "checkmark") } else { Text(option.label) }
                    }
                }
            }
        } label: {
            chip(setting.valueLabel)
        }
        .accessibilityLabel(name + ", " + setting.valueLabel)
    }

    private func chip(_ text: String) -> some View {
        Text(text).font(.caption.weight(.medium))
            .foregroundStyle(.primary)
            .lineLimit(1)
            .fixedSize()
            .padding(.horizontal, 12).frame(minHeight: 44)
            .background(Color.primary.opacity(0.05), in: Capsule())
    }
}
