import SwiftUI

struct InboxStopCard: View {
    var model: AgenthailModel
    var pane: SessionPane
    let stop: InboxStop
    let following: String?
    @State private var expanded = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: DesktopSpace.m) {
                VStack(alignment: .leading, spacing: DesktopSpace.xs) {
                    Text(DecisionGroup.stopped.title)
                        .font(.system(size: DesktopType.caption, weight: .semibold))
                        .foregroundStyle(DesktopPalette.text2)
                    Text(stop.title(name: model.sessionName))
                        .font(.system(size: DesktopType.display, weight: .semibold))
                        .foregroundStyle(DesktopPalette.text)
                    if let when = stop.at.flatMap(TimestampParser.date) {
                        Text(when, format: .relative(presentation: .named))
                            .font(.system(size: DesktopType.secondary))
                            .foregroundStyle(DesktopPalette.text2)
                    }
                }
                ForEach(stop.lines, id: \.self) { line in
                    Text(line).font(.system(size: DesktopType.body)).foregroundStyle(DesktopPalette.text).textSelection(.enabled)
                }
                if let message = model.queuedText(stop) {
                    VStack(alignment: .leading, spacing: DesktopSpace.xs) {
                        Text(message)
                            .font(.system(size: DesktopType.body))
                            .foregroundStyle(DesktopPalette.text)
                            .lineLimit(expanded ? nil : 4)
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(DesktopSpace.s)
                            .background(DesktopPalette.fill, in: RoundedRectangle(cornerRadius: DesktopRadius.control))
                            .accessibilityIdentifier("inbox-stop-message")
                        if message.count > 280 || message.filter(\.isNewline).count > 3 {
                            Button(expanded ? "Show less" : "Show all") { expanded.toggle() }
                                .buttonStyle(.link)
                                .accessibilityIdentifier("inbox-stop-message-toggle")
                        }
                    }
                }
                HStack(spacing: DesktopSpace.s) { actions }
            }
            .frame(maxWidth: DesktopColumns.reading, alignment: .leading)
            .padding(DesktopSpace.xl)
        }
    }

    @ViewBuilder
    private var actions: some View {
        switch stop {
        case .queued(let item):
            Button("Retry") { settle { model.retryQueued(item) } }.accessibilityIdentifier("inbox-stop-retry")
            Button("Cancel") { settle { model.cancelQueued(item) } }.accessibilityIdentifier("inbox-stop-cancel")
            Button("Open session") { pane.select(item.sessionId) }
        case .delivery(let item):
            if item.deletable == true {
                Button("Delete") { settle { model.deleteDeliveryProblem(item) } }.accessibilityIdentifier("inbox-stop-delete")
            } else {
                Button("Dismiss") { settle { model.dismissDeliveryProblem(item) } }.accessibilityIdentifier("inbox-stop-dismiss")
            }
            Button("Open session") { pane.select(item.sessionId) }
        case .replacement(let item):
            Button("Open") { pane.select(item.sessionId) }.accessibilityIdentifier("inbox-stop-open")
            Button("Acknowledge") { settle { model.acknowledgeLeadReplacement(item) } }.accessibilityIdentifier("inbox-stop-acknowledge")
        case .decision:
            EmptyView()
        }
    }

    private func settle(_ action: () -> Void) {
        action()
        pane.selectedDecisionID = following
    }

}
