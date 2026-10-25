import SwiftUI

struct TranscriptBlockView: View, @MainActor Equatable {
    let block: TranscriptBlock
    var onEdit = TranscriptRowAction<String>(nil)
    @State private var opened: RowChoice<Bool>?
    @Environment(\.transcriptRows) private var rows
    @Environment(\.holdTranscriptPlace) private var holdPlace
    @Environment(\.transcriptOverhang) private var overhang

    private var note: String? { block.truncated ? TimelineMetadata.shortened : nil }
    private var expanded: Bool { rows.current(opened) ?? rows.isOpen(block.id) ?? false }

    static func == (lhs: Self, rhs: Self) -> Bool { lhs.block == rhs.block && lhs.onEdit == rhs.onEdit }

    var body: some View {
        switch block.kind {
        case .user(let text, let files):
            UserBubble(text: text, receipt: note, time: TurnClock.time(block.timestamp), files: files, onEdit: onEdit.closure, row: block.id)
        case .assistant(let text):
            AssistantReply(text: text, note: note, footer: block.finished.map { TurnClock.footer(duration: $0, at: block.timestamp) })
        case .agent(let entry):
            if entry.agent.isSend {
                AgentSendRow(id: block.id, entry: entry)
            } else {
                AgentMessageRow(id: block.id, entry: entry)
            }
        case .agentFold(let entry):
            AgentFoldRow(id: block.id, entry: entry)
        case .tools(let items):
            ToolRunView(id: block.id, items: items)
                .padding(.trailing, -overhang)
        default:
            VStack(alignment: .leading, spacing: DesktopSpace.xs) {
                record
                if let note {
                    Text(note)
                        .font(.system(size: DesktopType.caption))
                        .foregroundStyle(DesktopPalette.muted)
                }
            }
        }
    }

    private func toggle() {
        holdPlace()
        let next = !expanded
        opened = rows.choice(next)
        rows.set(block.id, open: next)
    }

    @ViewBuilder private var record: some View {
        switch block.kind {
        case .user, .assistant, .agent, .agentFold, .tools:
            EmptyView()
        case .annotation(let text):
            Text(text)
                .font(.system(size: DesktopType.meta))
                .foregroundStyle(DesktopPalette.muted)
        case .notice(let text):
            Button(action: toggle) {
                HStack(spacing: DesktopSpace.s) {
                    Rectangle().fill(DesktopPalette.line).frame(width: 18, height: 1)
                    Text(text)
                        .lineLimit(expanded ? nil : 1)
                        .multilineTextAlignment(.leading)
                        .layoutPriority(1)
                    Rectangle().fill(DesktopPalette.line).frame(minWidth: 18, maxWidth: .infinity, maxHeight: 1)
                }
                .font(.system(size: DesktopType.meta))
                .foregroundStyle(DesktopPalette.muted)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help(expanded ? "" : text)
            .accessibilityValue(expanded ? "Expanded" : "Collapsed")
        case .image(let attachment):
            AttachmentImageView(attachment: attachment)
        case .instructions(let text):
            VStack(alignment: .leading, spacing: 6) {
                Button(action: toggle) {
                    HStack(spacing: 6) {
                        Image(systemName: expanded ? "chevron.down" : "chevron.right")
                        Text("Instructions")
                    }
                    .font(.system(size: DesktopType.meta))
                    .foregroundStyle(DesktopPalette.muted)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityHint("Shows what the agent was told")
                if expanded {
                    Text(text)
                        .font(.system(size: DesktopType.meta, design: .monospaced))
                        .foregroundStyle(DesktopPalette.muted)
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
        }
    }
}
