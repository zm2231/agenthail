import SwiftUI

struct ComposerReceipt: View {
    @ObservedObject var model: AgenthailIOSModel
    let sessionID: String
    let status: String

    var body: some View {
        NavigationLink {
            QueueListView(model: model, sessionID: sessionID)
        } label: {
            HStack(spacing: 6) {
                Text(status)
                    .lineLimit(2)
                    .multilineTextAlignment(.leading)
                Spacer(minLength: 0)
                Image(systemName: "chevron.right").font(.caption2.weight(.semibold))
            }
            .font(.footnote)
            .foregroundStyle(PhonePalette.muted)
            .padding(.horizontal, 8)
            .frame(minHeight: 44, alignment: .leading)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Latest instruction: " + status)
        .accessibilityHint("Open this session’s delivery inbox")
        .accessibilityIdentifier("latest-instruction")
    }
}
