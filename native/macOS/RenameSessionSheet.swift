import SwiftUI

struct RenameSessionSheet: View {
    @ObservedObject var model: AgenthailModel
    let session: SessionState
    @Environment(\.dismiss) private var dismiss
    @State private var text: String
    @State private var saving = false
    @State private var error: String?

    init(model: AgenthailModel, session: SessionState) {
        self.model = model
        self.session = session
        _text = State(initialValue: session.alias ?? "")
    }

    private var problem: String? { SessionHandle.problem(with: text) }
    private var unchanged: Bool { SessionHandle.normalized(text) == (session.alias ?? "") }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Rename \(session.title)")
                .font(.headline)
            Text("Other agents and links use this name to reach the session.")
                .font(.system(size: 12))
                .foregroundStyle(DesktopPalette.text2)
            HStack(spacing: 4) {
                Text("@").foregroundStyle(DesktopPalette.text2)
                TextField("name", text: $text)
                    .textFieldStyle(.roundedBorder)
                    .onSubmit(save)
            }
            if let message = error ?? (text.isEmpty ? nil : problem) {
                Text(message)
                    .font(.system(size: 12))
                    .foregroundStyle(DesktopPalette.red)
            }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button(saving ? "Saving…" : "Save", action: save)
                    .keyboardShortcut(.defaultAction)
                    .disabled(saving || problem != nil || unchanged)
            }
        }
        .padding(20)
        .frame(width: 380)
    }

    private func save() {
        guard !saving, problem == nil, !unchanged else { return }
        saving = true
        error = nil
        Task {
            let failure = await model.rename(session.id, to: SessionHandle.normalized(text))
            saving = false
            if let failure { error = failure } else { dismiss() }
        }
    }
}
