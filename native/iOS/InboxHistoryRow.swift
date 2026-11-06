import SwiftUI

struct InboxHistoryRow: View {
    let message: InboxMessage
    let busy: Bool
    let openSession: () -> Void
    let sendAgain: () -> Void

    private var envelope: PeerEnvelope { PeerEnvelope(message.message) }
    private var sender: String? { envelope.sender ?? message.senderLabel }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .firstTextBaseline) {
                Text(message.outcomeLabel).font(.subheadline.weight(.semibold)).foregroundStyle(PhonePalette.muted)
                Spacer()
                if let date = message.createdDate {
                    Text(date, format: .relative(presentation: .numeric, unitsStyle: .abbreviated)).font(.footnote).foregroundStyle(PhonePalette.muted)
                }
            }
            InboxSessionLink(title: message.target, identifier: "inbox-message-\(message.id)", open: openSession)
            if let sender { Text("From \(sender)").font(.footnote).foregroundStyle(PhonePalette.muted) }
            Text(envelope.body).font(.body).lineLimit(4).textSelection(.enabled)
            if message.truncated == true {
                Text("Only the start of this message was kept. The whole message is in the session.").font(.footnote).foregroundStyle(PhonePalette.muted)
            }
            if let reason = message.reason { Text(reason).font(.footnote).foregroundStyle(PhonePalette.muted) }
            DisclosureGroup("Message details") {
                Text(envelope.body).textSelection(.enabled)
                if let attempts = message.attempts { LabeledContent("Attempts", value: attempts.formatted()).font(.footnote) }
                if let value = message.sourceSessionId, !value.isEmpty, message.source == message.senderLabel { LabeledContent("From session", value: value).font(.footnote).textSelection(.enabled) }
            }.font(.footnote)
            if message.canSendAgain {
                Button("Send again", action: sendAgain).buttonStyle(.bordered).controlSize(.large).disabled(busy)
            }
        }.padding(.vertical, 6)
    }
}
