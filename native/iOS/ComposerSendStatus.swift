import SwiftUI

/// What the composer says about a message that was refused or has no answer yet.
struct ComposerSendStatus: View {
    @ObservedObject var model: AgenthailIOSModel
    let session: SessionState
    @State private var confirmingPutBack = false

    private var waiting: PendingSend? { model.pendingSends[session.id]?.first { $0.unconfirmed } }

    var body: some View {
        if let failure = model.failedSends[session.id] {
            row(failure, color: PhonePalette.failed, identifier: "send-failure") {
                Button("Retry") { model.send(to: session) }
                    .accessibilityIdentifier("send-retry")
            }
        } else if let waiting {
            row("Not confirmed yet · retrying", color: PhonePalette.muted, identifier: "send-unconfirmed") {
                Button("Put back") { confirmingPutBack = true }
                    .accessibilityIdentifier("send-put-back")
                    .confirmationDialog("It may already have arrived. Put it back in the composer?", isPresented: $confirmingPutBack, titleVisibility: .visible) {
                        Button("Put back") { model.putBack(waiting) }
                        Button("Keep retrying", role: .cancel) {}
                    }
            }
        }
    }

    private func row(_ text: String, color: some ShapeStyle, identifier: String, @ViewBuilder action: () -> some View) -> some View {
        HStack(spacing: 8) {
            Text(text)
                .lineLimit(3)
                .multilineTextAlignment(.leading)
                .foregroundStyle(color)
                .accessibilityIdentifier(identifier)
            Spacer(minLength: 0)
            action().buttonStyle(.bordered).controlSize(.small).frame(minHeight: 44)
        }
        .font(.footnote)
        .padding(.horizontal, 8)
    }
}
