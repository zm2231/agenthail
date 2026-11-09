import SwiftUI

struct QueuedMessageSheet: View {
    let item: QueueState
    var reason: String?
    @Environment(\.dismiss) private var dismiss

    private var details: [(name: String, value: String)] {
        let optional: [(String, String?)] = [("Model", item.model), ("Service tier", item.serviceTier)]
        return [("Attempts", item.attempts.formatted())] + optional.compactMap { name, value in value.map { (name, $0) } }
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    Text(PeerEnvelope(item.message).body).font(.body).lineSpacing(4).textSelection(.enabled)
                    if item.status == "pending" {
                        Text(item.holdLabel).font(.footnote).foregroundStyle(PhonePalette.muted)
                            .accessibilityIdentifier("queued-message-hold")
                    }
                    if let reason = reason ?? item.lastError, !reason.isEmpty {
                        Text(reason).font(.footnote).foregroundStyle(PhonePalette.muted).textSelection(.enabled)
                            .accessibilityIdentifier("queued-message-reason")
                    }
                    VStack(spacing: 6) {
                        ForEach(details, id: \.name) { detail in
                            LabeledContent(detail.name, value: detail.value).font(.footnote)
                        }
                    }
                }
                .frame(maxWidth: SessionStyle.readingWidth, alignment: .leading)
                .frame(maxWidth: .infinity, alignment: .center)
                .padding(20)
            }
            .navigationTitle("Queued message")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            }
        }
    }
}
