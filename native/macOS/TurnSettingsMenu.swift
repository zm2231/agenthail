import SwiftUI

struct TurnSettingsMenu: View {
    @ObservedObject var model: AgenthailModel
    let sessionID: String
    let detail: SessionDetail?

    private var settings: TurnSettings { model.turnSettings(for: sessionID) }

    private var efforts: [String] {
        let options = detail?.models ?? []
        let current = options.first { $0.id == detail?.model } ?? (detail?.model == nil ? options.first { $0.default == true } : nil)
        return current?.supportedReasoningEfforts ?? []
    }

    var body: some View {
        Menu {
            if !efforts.isEmpty {
                Picker("Effort", selection: binding(\.effort)) {
                    Text("Runtime default").tag(nil as String?)
                    ForEach(efforts, id: \.self) { Text($0.capitalized).tag($0 as String?) }
                }
            }
            Picker("Mode", selection: binding(\.mode)) {
                Text("Session setting").tag(nil as TurnSettings.Mode?)
                Text("Work").tag(TurnSettings.Mode.default as TurnSettings.Mode?)
                Text("Plan").tag(TurnSettings.Mode.plan as TurnSettings.Mode?)
            }
            if !settings.isEmpty {
                Divider()
                Button("Reset") { model.setTurnSettings(TurnSettings(), for: sessionID) }
            }
        } label: {
            HStack(spacing: 4) {
                Image(systemName: "slider.horizontal.3")
                if let summary { Text(summary) }
            }
            .font(.system(size: 11.5))
            .foregroundStyle(settings.isEmpty ? DesktopPalette.text2 : DesktopPalette.accentText)
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
        .help("Effort and mode for the next message. Steering uses the running turn's settings.")
        .accessibilityLabel(summary.map { "Next turn: \($0)" } ?? "Next turn settings")
    }

    private var summary: String? {
        let parts = [settings.effort?.capitalized, settings.mode.map { $0 == .plan ? "Plan" : "Work" }].compactMap { $0 }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }

    private func binding<Value>(_ keyPath: WritableKeyPath<TurnSettings, Value>) -> Binding<Value> {
        Binding(
            get: { model.turnSettings(for: sessionID)[keyPath: keyPath] },
            set: { value in
                var next = model.turnSettings(for: sessionID)
                next[keyPath: keyPath] = value
                model.setTurnSettings(next, for: sessionID)
            })
    }
}
