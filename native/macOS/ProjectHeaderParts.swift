import SwiftUI

struct ProjectLeadChipView: View {
    let chip: ProjectLeadChip
    let open: (String) -> Void

    var body: some View {
        switch chip {
        case let .conflict(label, help):
            Label(label, systemImage: "exclamationmark.triangle")
                .foregroundStyle(DesktopPalette.attention)
                .chipFrame()
                .help(help)
                .accessibilityLabel("\(label). \(help)")
                .accessibilityIdentifier("project-lead-chip")
        case let .waiting(label, help):
            Text(label)
                .foregroundStyle(DesktopPalette.text2)
                .chipFrame()
                .help(help)
                .accessibilityLabel("\(label), \(help)")
                .accessibilityIdentifier("project-lead-chip")
        case let .open(label, help, sessionID):
            Button(label) { open(sessionID) }
                .buttonStyle(.plain)
                .foregroundStyle(DesktopPalette.accentText)
                .chipFrame()
                .help(help)
                .accessibilityIdentifier("project-lead-chip")
        }
    }
}

private extension View {
    func chipFrame() -> some View {
        font(.system(size: DesktopType.secondary))
            .padding(.horizontal, 8)
            .padding(.vertical, 2)
            .overlay(Capsule().strokeBorder(DesktopPalette.line))
    }
}

struct ProjectBroadcastButton: View {
    let project: ProjectState
    let send: (String, String) async throws -> String
    @State private var composing = false
    @State private var text = ""
    @State private var key = UUID().uuidString
    @State private var sending = false
    @State private var problem: String?
    @State private var sent: String?

    var body: some View {
        HStack(spacing: DesktopSpace.s) {
            if let sent {
                Text(sent)
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(DesktopPalette.text2)
                    .lineLimit(1)
                    .truncationMode(.tail)
                    .help(sent)
                    .textSelection(.enabled)
            }
            Button("Message…") {
                sent = nil
                composing = true
            }
            .help("Send one message to every active conversation in \(project.displayName)")
        }
        .sheet(isPresented: $composing) { sheet }
        .onChange(of: text) { key = UUID().uuidString }
    }

    private var sheet: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text(ProjectHeaderFacts.broadcastPrompt(project)).font(.headline)
            TextEditor(text: $text)
                .font(.body)
                .frame(minHeight: 100)
                .overlay(RoundedRectangle(cornerRadius: DesktopRadius.control).stroke(DesktopPalette.line))
                .accessibilityLabel("Message")
            if let problem {
                Text(problem)
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(DesktopPalette.failed)
                    .textSelection(.enabled)
            }
            HStack {
                Spacer()
                Button("Cancel", role: .cancel) { composing = false }
                    .keyboardShortcut(.cancelAction)
                    .disabled(sending)
                Button(sending ? "Sending…" : "Send") { Task { await submit() } }
                    .keyboardShortcut(.return, modifiers: .command)
                    .disabled(sending || text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
        .padding(DesktopSpace.xl)
        .frame(width: 420)
        .interactiveDismissDisabled(sending)
    }

    private func submit() async {
        sending = true
        problem = nil
        defer { sending = false }
        do {
            sent = try await send(text, key)
            text = ""
            key = UUID().uuidString
            composing = false
        } catch {
            problem = AgenthailAPIError.userMessage(error)
        }
    }
}
