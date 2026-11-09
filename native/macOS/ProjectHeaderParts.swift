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
        .sheet(isPresented: $composing) {
            ProjectMessageSheet(project: project, send: send) { sent = $0 }
        }
    }
}
