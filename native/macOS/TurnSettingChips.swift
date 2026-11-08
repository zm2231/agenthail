import SwiftUI

struct TurnSettingChips: View {
    var pane: SessionPane
    @State private var pending: TurnSettingChoice?

    private var settings: [TurnSetting] { pane.detail?.turnSettings ?? [] }

    var body: some View {
        HStack(spacing: DesktopSpace.xs) {
            ForEach([TurnSetting.model, TurnSetting.effort, TurnSetting.mode], id: \.self) { key in
                if let setting = settings.setting(key) { chip(setting) }
            }
        }
        .disabled(pane.controlPending)
        .confirmingTurnSetting($pending) { pane.changeTurnSetting($0.key, to: $0.option.id) }
    }

    private func chip(_ setting: TurnSetting) -> some View {
        Menu { options(setting) } label: { label(setting) }
            .menuStyle(.button)
            .buttonStyle(.plain)
            .menuIndicator(.hidden)
            .fixedSize()
            .help(setting.appliesNextTurn ? "Applies from the next turn" : "Applies now")
            .accessibilityLabel(setting.chipAccessibilityLabel)
    }

    @ViewBuilder private func options(_ setting: TurnSetting) -> some View {
        Picker(setting.chipName, selection: selection(setting)) {
            ForEach(menuOptions(setting), id: \.id) { option in
                Text(option.label).tag(option.id)
            }
        }
        .pickerStyle(.inline)
        .labelsHidden()
        if setting.appliesNextTurn {
            Divider()
            Text("Applies from the next turn")
        }
    }

    private func label(_ setting: TurnSetting) -> some View {
        HStack(spacing: 3) {
            Text(setting.valueLabel)
                .lineLimit(1)
            Image(systemName: "chevron.up.chevron.down")
                .imageScale(.small)
        }
        .font(.system(size: DesktopType.meta, weight: .medium))
        .foregroundStyle(DesktopPalette.text2)
        .fixedSize()
        .contentShape(Rectangle())
    }

    private func menuOptions(_ setting: TurnSetting) -> [TurnSetting.Option] {
        pane.detail?.menuOptions(setting) ?? setting.options
    }

    private func selection(_ setting: TurnSetting) -> Binding<String> {
        Binding(get: { setting.value }) { id in
            guard let option = menuOptions(setting).first(where: { $0.id == id }) else { return }
            TurnSettingChoice.choose(option, for: setting, pending: &pending, apply: pane.changeTurnSetting)
        }
    }
}
