import SwiftUI

struct IOSCompactSettingRows: View {
    @ObservedObject var model: AgenthailIOSModel
    let sessionID: String
    let settings: [TurnSetting]

    var body: some View {
        ForEach(settings, id: \.key) { setting in
            Picker(setting.chipName, selection: selection(setting)) {
                if setting.current == nil { Text(setting.valueLabel).tag(setting.value) }
                ForEach(setting.quietOptions, id: \.id) { option in
                    Text(option.label).tag(option.id)
                }
            }
            .pickerStyle(.menu)
            .accessibilityIdentifier("compact-setting-" + setting.key)
            if let note = setting.appliesNote {
                Text(note).font(.footnote).foregroundStyle(PhonePalette.muted)
            }
        }
        .disabled(model.pendingControls.contains(sessionID))
    }

    private func selection(_ setting: TurnSetting) -> Binding<String> {
        Binding(get: { setting.value }) { id in
            guard let option = setting.quietOptions.first(where: { $0.id == id }), setting.pick(option) == .apply else { return }
            Task { await model.setTurnSetting(setting.key, to: id, sessionID: sessionID) }
        }
    }
}
