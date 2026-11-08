import SwiftUI

struct TurnSettingChoice: Identifiable, Equatable {
    let key: String
    let option: TurnSetting.Option

    var id: String { key + "=" + option.id }

    static func choose(_ option: TurnSetting.Option, for setting: TurnSetting, pending: inout TurnSettingChoice?, apply: (String, String) -> Void) {
        switch setting.pick(option) {
        case .unchanged: break
        case .apply: apply(setting.key, option.id)
        case .confirm: pending = TurnSettingChoice(key: setting.key, option: option)
        }
    }
}

extension View {
    func confirmingTurnSetting(_ pending: Binding<TurnSettingChoice?>, choose: @escaping (TurnSettingChoice) -> Void) -> some View {
        confirmationDialog(
            pending.wrappedValue.map { "Switch to \($0.option.label)?" } ?? "",
            isPresented: Binding(get: { pending.wrappedValue != nil }, set: { if !$0 { pending.wrappedValue = nil } }),
            titleVisibility: .visible,
            presenting: pending.wrappedValue
        ) { choice in
            Button(choice.option.label, role: .destructive) { choose(choice) }
            Button("Cancel", role: .cancel) {}
        } message: { _ in
            Text("The agent will run tools without asking you first.")
        }
    }
}
