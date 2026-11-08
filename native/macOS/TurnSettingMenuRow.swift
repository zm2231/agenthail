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
            Text(title ?? setting.chipName).foregroundStyle(DesktopPalette.text2)
            VStack(alignment: .leading, spacing: 2) {
                Menu(setting.valueLabel) {
                    Picker(setting.chipName, selection: selection) {
                        ForEach(choices, id: \.id) { option in
                            Text(option.label).tag(option.id)
                        }
                    }
                    .pickerStyle(.inline)
                    .labelsHidden()
                }
                .menuStyle(.borderlessButton)
                .fixedSize()
                .disabled(disabled)
                .accessibilityLabel(setting.chipAccessibilityLabel)
                if let note = setting.appliesNote {
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
