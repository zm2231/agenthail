import SwiftUI

struct TurnSettingPicker: View {
    var model: AgenthailIOSModel
    let sessionID: String
    let setting: TurnSetting
    let options: [TurnSetting.Option]

    var body: some View {
        Section(setting.appliesNextTurn ? "From the next turn" : "") {
            Picker(setting.chipName, selection: selection) {
                ForEach(options, id: \.id) { option in
                    Text(option.label).tag(option.id)
                }
            }
            .pickerStyle(.inline)
            .labelsHidden()
        }
    }

    private var selection: Binding<String> {
        Binding(get: { setting.value }) { id in
            guard let option = options.first(where: { $0.id == id }), setting.pick(option) == .apply else { return }
            Task { await model.setTurnSetting(setting.key, to: option.id, sessionID: sessionID) }
        }
    }
}
