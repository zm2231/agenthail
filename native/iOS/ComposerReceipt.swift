import SwiftUI

struct ComposerReceipt: View {
    let status: String
    let showQueuedRow: (() -> Void)?

    var body: some View {
        if let showQueuedRow {
            Button(action: showQueuedRow) {
                HStack(spacing: 6) {
                    message
                    Spacer(minLength: 0)
                    Image(systemName: "chevron.down").font(.caption2.weight(.semibold))
                }
                .font(.footnote)
                .foregroundStyle(PhonePalette.muted)
                .padding(.horizontal, 8)
                .frame(minHeight: 44, alignment: .leading)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Latest instruction: " + status)
            .accessibilityHint("Shows the queued message")
            .accessibilityIdentifier("latest-instruction")
        } else {
            message
                .font(.footnote)
                .foregroundStyle(PhonePalette.muted)
                .padding(.horizontal, 8)
                .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
                .accessibilityLabel("Latest instruction: " + status)
                .accessibilityIdentifier("latest-instruction")
        }
    }

    private var message: some View {
        Text(status)
            .lineLimit(2)
            .multilineTextAlignment(.leading)
    }
}
