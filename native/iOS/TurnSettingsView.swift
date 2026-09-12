import SwiftUI

struct TurnSettingsView: View {
    @Binding var settings: TurnSettings
    let modelOption: ModelOption?
    let enabled: Bool

    private var efforts: [String] { modelOption?.supportedReasoningEfforts ?? [] }

    var body: some View {
        Section("Next Codex turn") {
            Picker("Effort", selection: effortBinding) {
                Text("Runtime default").tag("")
                ForEach(efforts, id: \.self) { Text($0.capitalized).tag($0) }
            }
            Picker("Mode", selection: modeBinding) {
                Text("Use session setting").tag(nil as TurnSettings.Mode?)
                Text("Work").tag(TurnSettings.Mode.default as TurnSettings.Mode?)
                Text("Plan").tag(TurnSettings.Mode.plan as TurnSettings.Mode?)
            }
            if !enabled {
                Text("Turn settings apply to the next normal message. Steering uses the active turn.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            } else if efforts.isEmpty {
                Text("This runtime did not report selectable effort values.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
        }
        .disabled(!enabled)
    }

    private var effortBinding: Binding<String> {
        Binding(
            get: { settings.effort ?? "" },
            set: { settings.effort = $0.isEmpty ? nil : $0 })
    }

    private var modeBinding: Binding<TurnSettings.Mode?> {
        Binding(
            get: { settings.mode },
            set: { settings.mode = $0 })
    }
}
