import SwiftUI

struct QueueItemActions: View {
    @ObservedObject var model: AgenthailModel
    let item: QueueState

    var body: some View {
        if QueueFacts.canRetry(item) {
            Button("Retry") { model.perform(action: "queue-retry", queueID: item.id) }
        }
        if QueueFacts.canCancel(item) {
            Button("Cancel") { model.perform(action: "queue-cancel", queueID: item.id) }
        }
    }
}

struct QueueItemRow: View {
    @ObservedObject var model: AgenthailModel
    let item: QueueState

    var body: some View {
        let target = model.knownSessions.first { $0.id == item.sessionId }
        let lines = QueueFacts.detail(item, target: target, attention: model.snapshot?.attention ?? [])
        HStack(alignment: .top) {
            VStack(alignment: .leading, spacing: 2) {
                Text(item.target)
                Text(PeerEnvelope(item.message).summary)
                    .font(.system(size: 12))
                    .foregroundStyle(DesktopPalette.text2)
                    .lineLimit(2)
                ForEach(Array(lines.enumerated()), id: \.offset) { index, line in
                    Text(line)
                        .font(.system(size: 11.5))
                        .foregroundStyle(index == lines.count - 1 && QueueFacts.lastError(item) != nil ? DesktopPalette.red : DesktopPalette.text2)
                        .lineLimit(3)
                        .textSelection(.enabled)
                }
                Text("Queued \(relativeAge(item.queuedAt))")
                    .font(.system(size: 11))
                    .foregroundStyle(DesktopPalette.muted)
            }
            Spacer()
            Text(QueueFacts.statusLabel(item.status))
                .font(.system(size: 11))
                .foregroundStyle(item.status == "dead" ? DesktopPalette.red : DesktopPalette.text2)
            QueueItemActions(model: model, item: item)
        }
    }
}

struct AttentionBanner: View {
    @ObservedObject var model: AgenthailModel
    let attention: AttentionState

    var body: some View {
        let item = model.snapshot?.queue.first { $0.id == attention.queueId }
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: "exclamationmark.triangle")
                .foregroundStyle(DesktopPalette.amber)
            VStack(alignment: .leading, spacing: 3) {
                Text(attention.reason)
                    .font(.system(size: 12.5, weight: .medium))
                Text(attention.requestedAction)
                    .foregroundStyle(DesktopPalette.text2)
                if let item {
                    Text(PeerEnvelope(item.message).body)
                        .lineLimit(2)
                        .foregroundStyle(DesktopPalette.text2)
                    HStack(spacing: 14) {
                        QueueItemActions(model: model, item: item)
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(DesktopPalette.accentText)
                    .padding(.top, 2)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .font(.system(size: 12.5))
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
        .background(DesktopPalette.warnBackground, in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(DesktopPalette.warnLine))
    }
}
