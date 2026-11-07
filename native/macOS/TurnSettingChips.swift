import SwiftUI

struct TurnSettingChips: View {
    @ObservedObject var pane: SessionPane
    let cycleMode: () -> Void
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

    @ViewBuilder private func chip(_ setting: TurnSetting) -> some View {
        if setting.key == TurnSetting.mode {
            Menu { options(setting) } label: { label(setting) } primaryAction: { cycleMode() }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .fixedSize()
                .help("Click to switch mode, hold for every mode")
                .accessibilityLabel("Mode, \(setting.valueLabel)")
        } else {
            Menu { options(setting) } label: { label(setting) }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .fixedSize()
                .help(setting.appliesNextTurn ? "Applies from the next turn" : "Applies now")
                .accessibilityLabel("\(setting.key.capitalized), \(setting.valueLabel)")
        }
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
        Text(setting.valueLabel)
            .font(.system(size: DesktopType.meta, weight: .medium))
            .foregroundStyle(DesktopPalette.text2)
            .lineLimit(1)
            .fixedSize()
    }

    private func choose(_ option: TurnSetting.Option, for setting: TurnSetting) {
        TurnSettingChoice.choose(option, for: setting, pending: &pending, apply: pane.changeTurnSetting)
    }
}
