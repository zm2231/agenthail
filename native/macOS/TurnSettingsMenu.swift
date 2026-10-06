import SwiftUI

struct TurnSettingsMenu: View {
    @ObservedObject var model: AgenthailModel
    let sessionID: String
    let detail: SessionDetail?
    @State private var editingSchema = false

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
            Picker("Service tier", selection: binding(\.serviceTier)) {
                Text("Session setting").tag(nil as TurnSettings.ServiceTier?)
                ForEach(TurnSettings.ServiceTier.allCases, id: \.self) { tier in
                    Text(tier.rawValue.capitalized).tag(tier as TurnSettings.ServiceTier?)
                }
            }
            Button(settings.outputSchema == nil ? "Output schema…" : "Edit output schema…") { editingSchema = true }
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
        .help("Effort, mode, service tier and output schema for the next message. Steering uses the running turn's settings.")
        .accessibilityLabel(summary.map { "Next turn: \($0)" } ?? "Next turn settings")
        .sheet(isPresented: $editingSchema) {
            OutputSchemaSheet(schema: settings.outputSchema) { schema in
                var next = model.turnSettings(for: sessionID)
                next.outputSchema = schema
                model.setTurnSettings(next, for: sessionID)
            }
        }
    }

    private var summary: String? {
        let parts = [
            settings.effort?.capitalized,
            settings.mode.map { $0 == .plan ? "Plan" : "Work" },
            settings.serviceTier.map { $0.rawValue.capitalized },
            settings.outputSchema.map { _ in "Schema" },
        ].compactMap { $0 }
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

struct OutputSchemaSheet: View {
    let save: (RecordedJSON?) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var text: String
    @State private var error: String?

    init(schema: RecordedJSON?, save: @escaping (RecordedJSON?) -> Void) {
        self.save = save
        _text = State(initialValue: schema?.formatted ?? "")
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Output schema")
                .font(.headline)
            Text("Codex shapes its final reply for the next message to match this JSON Schema object. Leave empty to remove it.")
                .font(.system(size: 12))
                .foregroundStyle(DesktopPalette.text2)
                .fixedSize(horizontal: false, vertical: true)
            TextEditor(text: $text)
                .font(.system(size: 12, design: .monospaced))
                .frame(minHeight: 160)
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(DesktopPalette.line))
            if let error {
                Text(error)
                    .font(.system(size: 12))
                    .foregroundStyle(DesktopPalette.red)
            }
            HStack {
                Spacer()
                Button("Cancel", role: .cancel) { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button("Save", action: commit)
                    .keyboardShortcut(.return, modifiers: .command)
            }
        }
        .padding(20)
        .frame(width: 460)
    }

    private func commit() {
        do {
            save(try TurnSettings.outputSchema(from: text))
            dismiss()
        } catch {
            self.error = error.localizedDescription
        }
    }
}
