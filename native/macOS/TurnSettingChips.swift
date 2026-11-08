import SwiftUI

struct TurnSettingChips: View {
    @ObservedObject var pane: SessionPane
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
            .accessibilityLabel("\(setting.key.capitalized), \(setting.valueLabel)")
    }

    @ViewBuilder private func options(_ setting: TurnSetting) -> some View {
        ForEach(setting.options, id: \.id) { option in
            Button {
                choose(option, for: setting)
            } label: {
                if option.id == setting.value { Label(option.label, systemImage: "checkmark") } else { Text(option.label) }
            }
        }
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

    private func choose(_ option: TurnSetting.Option, for setting: TurnSetting) {
        TurnSettingChoice.choose(option, for: setting, pending: &pending, apply: pane.changeTurnSetting)
    }
}
