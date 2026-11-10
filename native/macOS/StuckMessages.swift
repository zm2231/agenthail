import SwiftUI

struct QueueItemActions: View {
    var model: AgenthailModel
    let item: QueueState
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        if QueueFacts.canCancel(item) {
            Button("Cancel") { model.perform(action: "queue-cancel", queueID: item.id) }
        }
        if let decision = model.decisionFeed.inbox.decision(answering: item.askId) {
            Button("Answer") {
                model.mainPane.showInbox()
                model.mainPane.selectedDecisionID = decision.id
                MainWindowShow.show(model.mainWindow) { openWindow(id: "main") }
            }
            .help("Open the decision this message is waiting on")
        }
    }
}

struct QueueItemRow: View {
    var model: AgenthailModel
    let item: QueueState
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        let target = model.knownSessions.first { $0.id == item.sessionId }
        let lines = QueueFacts.detail(item, target: target)
        HStack(alignment: .top) {
            VStack(alignment: .leading, spacing: 2) {
                Text(item.target)
                Text(PeerEnvelope(item.message).summary)
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(DesktopPalette.text2)
                    .lineLimit(2)
                ForEach(Array(lines.enumerated()), id: \.offset) { index, line in
                    Text(line)
                        .font(.system(size: DesktopType.meta))
                        .foregroundStyle(index == lines.count - 1 && QueueFacts.lastError(item) != nil ? DesktopPalette.failed : DesktopPalette.text2)
                        .lineLimit(3)
                        .textSelection(.enabled)
                }
                Text("Queued \(relativeAge(item.queuedAt))")
                    .font(.system(size: DesktopType.caption))
                    .foregroundStyle(DesktopPalette.muted)
            }
            Spacer()
            Text(QueueFacts.statusLabel(item.status))
                .font(.system(size: DesktopType.caption))
                .foregroundStyle(item.status == "dead" ? DesktopPalette.failed : DesktopPalette.text2)
            if QueueFacts.canTakeBack(item, target: target) {
                Button("Take back", action: takeBack)
                    .help("Cancel this queued message and put its text back in the conversation's composer")
            }
            QueueItemActions(model: model, item: item)
        }
    }

    private func takeBack() {
        model.takeBackQueued([item], sessionID: item.sessionId)
        model.mainPane.select(item.sessionId)
        MainWindowShow.show(model.mainWindow) { openWindow(id: "main") }
    }
}
