import SwiftUI

struct LaunchSettingsView: View {
    @Binding var settings: LaunchSettings
    let modelOption: ModelOption?
    let enabled: Bool

    private var efforts: [String] { modelOption?.supportedReasoningEfforts ?? [] }

    var body: some View {
        Section("First Codex turn") {
            Picker("Effort", selection: effortBinding) {
                Text("Runtime default").tag("")
                ForEach(efforts, id: \.self) { Text($0.capitalized).tag($0) }
            }
            Picker("Mode", selection: $settings.mode) {
                Text("Runtime default").tag(nil as LaunchSettings.Mode?)
                Text("Work").tag(LaunchSettings.Mode.default as LaunchSettings.Mode?)
                Text("Plan").tag(LaunchSettings.Mode.plan as LaunchSettings.Mode?)
            }
            if !enabled {
                Text("A session started in a terminal uses the runtime's own effort and mode.")
                    .font(.footnote)
                    .foregroundStyle(PhonePalette.muted)
            } else if efforts.isEmpty {
                Text("This runtime did not report selectable effort values.")
                    .font(.footnote)
                    .foregroundStyle(PhonePalette.muted)
            }
        }
        .disabled(!enabled)
    }

    private var effortBinding: Binding<String> {
        Binding(
            get: { settings.effort ?? "" },
            set: { settings.effort = $0.isEmpty ? nil : $0 })
    }
}
