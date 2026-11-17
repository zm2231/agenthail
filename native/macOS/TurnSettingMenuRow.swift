import SwiftUI

struct TurnSettingMenuRow: View {
    let setting: TurnSetting
    var title: String? = nil
    var options: [TurnSetting.Option]? = nil
    let disabled: Bool
    let choose: (TurnSetting.Option) -> Void

    private var choices: [TurnSetting.Option] { options ?? setting.options }

    var body: some View {
        GridRow {
            Text(title ?? InspectorSettingText.title(setting))
                .foregroundStyle(DesktopPalette.text2)
                .fixedSize(horizontal: false, vertical: true)
            VStack(alignment: .leading, spacing: 2) {
                Menu {
                    Picker(setting.chipName, selection: selection) {
                        ForEach(choices, id: \.id) { option in
                            Text(InspectorSettingText.label(option, in: setting)).tag(option.id)
                        }
                    }
                    .pickerStyle(.inline)
                    .labelsHidden()
                } label: {
                    HStack(spacing: 3) {
                        Text(InspectorSettingText.value(setting))
                        Image(systemName: "chevron.up.chevron.down")
                            .font(.system(size: DesktopType.caption - 2, weight: .semibold))
                    }
                    .foregroundStyle(disabled ? DesktopPalette.text2 : DesktopPalette.accentText)
                    .contentShape(Rectangle())
                }
                .menuStyle(.button)
                .buttonStyle(.plain)
                .menuIndicator(.hidden)
                .fixedSize()
                .disabled(disabled)
                .accessibilityLabel("\(title ?? InspectorSettingText.title(setting)), \(InspectorSettingText.value(setting))")
                if let note = InspectorSettingText.note(setting) {
                    Text(note)
                        .font(.system(size: DesktopType.meta))
                        .foregroundStyle(DesktopPalette.text2)
                }
            }
        }
    }

    private var selection: Binding<String> {
        Binding(get: { setting.value }) { id in
            if let option = choices.first(where: { $0.id == id }) { choose(option) }
        }
    }
}
