import SwiftUI

struct ProjectMessageSheet: View {
    let project: ProjectState
    let send: (String, String) async throws -> String
    var onSent: ((String) -> Void)? = nil
    @Environment(\.dismiss) private var dismiss
    @State private var text = ""
    @State private var key = UUID().uuidString
    @State private var sending = false
    @State private var problem: String?
    @State private var sent: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text(ProjectBroadcast.prompt(project)).font(.headline)
            if let sent {
                Text(sent)
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(DesktopPalette.text2)
                    .textSelection(.enabled)
                HStack {
                    Spacer()
                    Button("Done") { dismiss() }
                        .keyboardShortcut(.defaultAction)
                }
            } else {
                compose
            }
        }
        .padding(DesktopSpace.xl)
        .frame(width: 420)
        .interactiveDismissDisabled(sending)
        .onChange(of: text) { key = UUID().uuidString }
    }

    @ViewBuilder private var compose: some View {
        TextEditor(text: $text)
            .font(.body)
            .frame(minHeight: 100)
            .overlay(RoundedRectangle(cornerRadius: DesktopRadius.control).stroke(DesktopPalette.line))
            .accessibilityLabel("Message")
            .disabled(sending)
        if let problem {
            Text(problem)
                .font(.system(size: DesktopType.secondary))
                .foregroundStyle(DesktopPalette.failed)
                .textSelection(.enabled)
        }
        HStack {
            Spacer()
            Button("Cancel", role: .cancel) { dismiss() }
                .keyboardShortcut(.cancelAction)
                .disabled(sending)
            Button(sending ? "Sending…" : "Send") { Task { await submit() } }
                .keyboardShortcut(.return, modifiers: .command)
                .disabled(sending || text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
        }
    }

    private func submit() async {
        sending = true
        problem = nil
        defer { sending = false }
        do {
            let summary = try await send(text, key)
            text = ""
            key = UUID().uuidString
            if let onSent {
                onSent(summary)
                dismiss()
            } else {
                sent = summary
            }
        } catch {
            problem = AgenthailAPIError.userMessage(error)
        }
    }
}
