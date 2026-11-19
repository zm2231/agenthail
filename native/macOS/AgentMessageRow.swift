import SwiftUI
import Textual

struct AgentMessageRow: View {
    let id: String
    let entry: AgentEntry
    @State private var opened: RowChoice<Bool>?
    @State private var truncated = false
    @State private var hovering = false
    @Environment(\.transcriptRows) private var rows
    @Environment(\.holdTranscriptPlace) private var holdPlace
    @Environment(\.expandAgentMessages) private var expandAll

    private var files: [AttachedFile] { entry.item.files ?? [] }
    private var expandable: Bool { truncated || !files.isEmpty || TranscriptLinks.hasAddress(entry.item.text) }
    private var expanded: Bool { expandAll || (rows.current(opened) ?? rows.isOpen(id) ?? false) }

    var body: some View {
        VStack(alignment: .leading, spacing: DesktopSpace.xs) {
            if expanded { header } else { preview }
            if expanded {
                VStack(alignment: .leading, spacing: DesktopSpace.s) {
                    if !entry.item.text.isEmpty {
                        InlineText(entry.item.text, parser: .linkedPlain)
                            .font(.system(size: ReadingType.prose))
                            .lineSpacing(ReadingType.proseLineSpacing)
                            .foregroundStyle(DesktopPalette.text)
                            .desktopInlineStyle()
                            .textual.textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    ForEach(Array(files.enumerated()), id: \.offset) { _, file in AttachedFileChip(file: file) }
                    if entry.item.truncated {
                        Text(TimelineMetadata.shortened).font(.system(size: ReadingType.caption)).foregroundStyle(DesktopPalette.muted)
                    }
                }
                .padding(.leading, AgentRowMetrics.indent)
            }
        }
        .onHover { hovering = $0 }
        .accessibilityElement(children: .contain)
    }

    private var preview: some View {
        Button(action: toggle) {
            VStack(alignment: .leading, spacing: 2) {
                HStack(alignment: .firstTextBaseline, spacing: DesktopSpace.s) {
                    AgentRowMetrics.marker(open: false).opacity(expandable ? 1 : 0)
                    Text(entry.chip)
                        .font(.system(size: ReadingType.body, weight: .semibold))
                        .foregroundStyle(DesktopPalette.peer)
                    if let time = TurnClock.time(entry.item.timestamp) {
                        Text(time).font(.system(size: ReadingType.caption)).foregroundStyle(DesktopPalette.meta)
                    }
                    if expandable {
                        Text("Show more")
                            .font(.system(size: ReadingType.caption, weight: .medium))
                            .foregroundStyle(DesktopPalette.accentText)
                            .fixedSize()
                    }
                }
                ClampedText(text: Text(AgentMessage.inline(previewText)).foregroundStyle(DesktopPalette.text) + Text(fileNote).foregroundStyle(DesktopPalette.muted), lines: AgentRowMetrics.previewLines, truncated: $truncated)
                    .font(.system(size: ReadingType.prose))
                    .lineSpacing(ReadingType.proseLineSpacing)
                    .padding(.leading, AgentRowMetrics.indent)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel("\(entry.chip): \(entry.agent.summary)")
        .accessibilityValue(expandable ? "Collapsed" : "")
        .accessibilityHint(expandable ? "Shows the whole message" : "")
    }

    private var header: some View {
        HStack(alignment: .firstTextBaseline, spacing: DesktopSpace.s) {
            Button(action: toggle) {
                HStack(alignment: .firstTextBaseline, spacing: DesktopSpace.s) {
                    AgentRowMetrics.marker(open: true)
                    Text(entry.chip)
                        .font(.system(size: ReadingType.body, weight: .semibold))
                        .foregroundStyle(DesktopPalette.peer)
                    if let time = TurnClock.time(entry.item.timestamp) {
                        Text(time).font(.system(size: ReadingType.caption)).foregroundStyle(DesktopPalette.meta)
                    }
                    if !expandAll {
                        Text("Show less")
                            .font(.system(size: ReadingType.caption, weight: .medium))
                            .foregroundStyle(DesktopPalette.accentText)
                    }
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel("From \(entry.chip)")
            .accessibilityValue("Expanded")
            MessageActions(text: entry.item.text, revealed: hovering)
        }
    }

    private var previewText: String {
        let text = entry.item.text.trimmingCharacters(in: .whitespacesAndNewlines)
        return text.isEmpty ? entry.agent.summary : text
    }

    private var fileNote: String {
        files.isEmpty ? "" : files.count == 1 ? "  · 1 file" : "  · \(files.count) files"
    }

    private func toggle() {
        guard !expandAll, expandable || expanded else { return }
        holdPlace()
        let next = !expanded
        opened = rows.choice(next)
        rows.set(id, open: next)
    }
}

struct AgentSendRow: View {
    let id: String
    let entry: AgentEntry
    @State private var opened: RowChoice<Bool>?
    @Environment(\.transcriptRows) private var rows
    @Environment(\.holdTranscriptPlace) private var holdPlace
    @Environment(\.expandAgentMessages) private var expandAll

    private var message: String { entry.agent.body ?? "" }
    private var expanded: Bool { !message.isEmpty && (expandAll || (rows.current(opened) ?? rows.isOpen(id) ?? false)) }

    var body: some View {
        VStack(alignment: .leading, spacing: DesktopSpace.xs) {
            Button(action: toggle) {
                HStack(alignment: .firstTextBaseline, spacing: DesktopSpace.xs) {
                    Image(systemName: "arrow.turn.down.right")
                        .font(.system(size: ReadingType.caption))
                        .foregroundStyle(DesktopPalette.muted)
                    (Text("To \(entry.agent.chip): ").fontWeight(.medium) + Text(AgentMessage.inline(entry.agent.summary)))
                        .font(.system(size: ReadingType.meta))
                        .foregroundStyle(DesktopPalette.text2)
                        .lineLimit(1)
                    if entry.failed {
                        Text("failed").font(.system(size: ReadingType.meta, weight: .medium)).foregroundStyle(DesktopPalette.failed)
                    }
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Sent to \(entry.agent.chip): \(entry.agent.summary)\(entry.failed ? ", failed" : "")")
            .accessibilityValue(message.isEmpty ? "" : expanded ? "Expanded" : "Collapsed")
            if expanded {
                InlineText(message, parser: .linkedPlain)
                    .font(.system(size: ReadingType.body))
                    .lineSpacing(4)
                    .foregroundStyle(DesktopPalette.text2)
                    .desktopInlineStyle()
                    .textual.textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.leading, DesktopSpace.l)
            }
        }
        .padding(.leading, DesktopSpace.l)
    }

    private func toggle() {
        guard !expandAll, !message.isEmpty else { return }
        holdPlace()
        let next = !expanded
        opened = rows.choice(next)
        rows.set(id, open: next)
    }
}

struct AgentFoldRow: View {
    let id: String
    let entry: AgentFoldEntry
    @State private var opened: RowChoice<Bool>?
    @Environment(\.transcriptRows) private var rows
    @Environment(\.holdTranscriptPlace) private var holdPlace

    private var expanded: Bool { rows.current(opened) ?? rows.isOpen(id) ?? false }

    var body: some View {
        VStack(alignment: .leading, spacing: DesktopSpace.s) {
            Button(action: toggle) {
                HStack(alignment: .firstTextBaseline, spacing: DesktopSpace.s) {
                    AgentRowMetrics.marker(open: expanded)
                    Text(entry.label)
                        .font(.system(size: ReadingType.body, weight: .semibold))
                        .foregroundStyle(DesktopPalette.peer)
                        .fixedSize()
                    Text(entry.senders)
                        .font(.system(size: ReadingType.body))
                        .foregroundStyle(DesktopPalette.text2)
                        .lineLimit(1)
                    Spacer(minLength: 0)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel(entry.label)
            .accessibilityValue(expanded ? "Expanded" : "Collapsed")
            .accessibilityHint("From \(entry.senders)")
            if expanded {
                VStack(alignment: .leading, spacing: DesktopSpace.s) {
                    ForEach(entry.members) { member in
                        if case .agent(let agent) = member.kind { AgentMessageRow(id: member.id, entry: agent) }
                    }
                }
                .padding(.leading, AgentRowMetrics.indent)
            }
        }
    }

    private func toggle() {
        holdPlace()
        let next = !expanded
        opened = rows.choice(next)
        rows.set(id, open: next)
    }
}

enum AgentRowMetrics {
    static let indent: CGFloat = 16
    static let previewLines = 6

    static func marker(open: Bool) -> some View {
        Image(systemName: "chevron.right")
            .font(.system(size: 9, weight: .semibold))
            .foregroundStyle(DesktopPalette.muted)
            .rotationEffect(.degrees(open ? 90 : 0))
            .frame(width: 8)
            .accessibilityHidden(true)
    }
}
