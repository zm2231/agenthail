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
            Section(setting.appliesNextTurn ? "From the next turn" : "") {
                Picker(setting.chipName, selection: selection(setting, options: options)) {
                    ForEach(options, id: \.id) { option in
                        Text(option.label).tag(option.id)
                    }
                }
                .pickerStyle(.inline)
                .labelsHidden()
            }
        } label: {
            chip(setting.valueLabel)
        }
        .accessibilityLabel(setting.chipAccessibilityLabel)
    }

    private func selection(_ setting: TurnSetting, options: [TurnSetting.Option]) -> Binding<String> {
        Binding(get: { setting.value }) { id in
            guard let option = options.first(where: { $0.id == id }), setting.pick(option) == .apply else { return }
            Task { await model.setTurnSetting(setting.key, to: option.id, sessionID: sessionID) }
        }
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
