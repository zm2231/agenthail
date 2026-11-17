import SwiftUI

struct ParkedWorkList: View {
    let item: DecisionItem

    var body: some View {
        let work = item.coveredWork
        VStack(alignment: .leading, spacing: DesktopSpace.xs) {
            Text(work.count > 1 ? "Covers \(work.count) items" : "Work item")
                .font(.system(size: DesktopType.caption, weight: .semibold))
                .foregroundStyle(DesktopPalette.text2)
            ForEach(work, id: \.key) { entry in
                HStack(alignment: .firstTextBaseline, spacing: DesktopSpace.s) {
                    Text(entry.workTitle ?? entry.workItem ?? "")
                        .font(.system(size: DesktopType.secondary))
                        .foregroundStyle(DesktopPalette.text)
                        .lineLimit(3)
                    if entry.workTitle != nil, let id = entry.workItem {
                        Text(id)
                            .font(.system(size: DesktopType.meta))
                            .foregroundStyle(DesktopPalette.muted)
                            .fixedSize()
                    }
                }
            }
        }
        .accessibilityIdentifier("decision-covered-work")
    }
}

struct ParkedReply: View {
    var model: AgenthailModel
    let item: DecisionItem
    @State private var text = ""
    @State private var sending = false
    @State private var failure: String?

    var body: some View {
        let name = model.sessionName(item.sessionId)
        VStack(alignment: .leading, spacing: DesktopSpace.s) {
            if let sent = model.decisionFeed.replies[item.id] {
                Label("Sent to \(sent)", systemImage: "checkmark.circle")
                    .font(.system(size: DesktopType.secondary, weight: .medium))
                    .foregroundStyle(DesktopPalette.text2)
                    .accessibilityIdentifier("decision-reply-sent")
            }
            TextField("Answer \(name)", text: $text, axis: .vertical)
                .textFieldStyle(.roundedBorder)
                .lineLimit(2...6)
                .disabled(sending)
                .onSubmit(send)
                .accessibilityIdentifier("decision-reply")
            HStack(spacing: DesktopSpace.s) {
                Button(sending ? "Sending…" : "Send to \(name)", action: send)
                    .disabled(sending || text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    .accessibilityIdentifier("decision-reply-send")
                if let failure {
                    Text(failure)
                        .font(.system(size: DesktopType.secondary))
                        .foregroundStyle(DesktopPalette.failed)
                }
            }
        }
    }

    private func send() {
        let words = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !words.isEmpty, !sending else { return }
        let name = model.sessionName(item.sessionId)
        let id = item.id
        sending = true
        failure = nil
        model.send(DecisionInbox.reply(words, for: item), to: item.sessionId) { delivered in
            sending = false
            if delivered {
                text = ""
                model.decisionFeed.replied(id, to: name)
            } else {
                failure = "Couldn't send to \(name). Open the session to retry."
            }
        }
    }
}
