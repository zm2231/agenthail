import SwiftUI
import Textual
import UIKit

enum AgentFoldGroups {
    static let tightRunGap: CGFloat = 4

    static func repeated(_ groups: [TimelineGroup]) -> [TimelineGroup] {
        let keys = groups.map { group -> String? in
            guard group.items.count == 1, group.foldLabel == nil else { return nil }
            return group.items[0].agent?.repeatKey
        }
        var merged: [TimelineGroup] = []
        var next = 0
        for run in AgentRepeat.runs(keys) {
            merged += groups[next..<run.lowerBound]
            var group = groups[run.lowerBound]
            group.items = groups[run.upperBound - 1].items
            group.repeatsByItem = [group.items[0].id: run.count]
            merged.append(group)
            next = run.upperBound
        }
        merged += groups[next...]
        return merged
    }

    static func folded(_ groups: [TimelineGroup]) -> [TimelineGroup] {
        let shown = groups.filter { group in !group.items.allSatisfy { $0.kind == "done" } }
        let agents = shown.map { group -> AgentMessage? in
            guard group.items.count == 1, let agent = group.items[0].agent, agent.isIncoming else { return nil }
            return agent
        }
        var folded: [TimelineGroup] = []
        var next = 0
        for run in AgentFold.runs(agents) {
            folded += shown[next..<run.lowerBound]
            let messages = shown[run].flatMap { group in group.items.compactMap(\.agent).flatMap { Array(repeating: $0, count: group.repeats) } }
            var group = TimelineGroup(id: TranscriptRowID.fold(shown[run.lowerBound].id), items: shown[run].flatMap(\.items))
            group.repeatsByItem = shown[run].reduce(into: [:]) { merged, member in merged.merge(member.repeatsByItem) { $1 } }
            group.foldLabel = AgentFold.label(messages)
            group.foldSenders = AgentFold.senders(messages)
            folded.append(group)
            next = run.upperBound
        }
        folded += shown[next...]
        return folded
    }
}

private func agentChip(_ item: TimelineItem, _ agent: AgentMessage) -> String {
    guard agent.kind == "task", let status = item.status, !status.isEmpty else { return agent.chip }
    return "\(agent.chip) · \(status)"
}

struct IOSAgentMessageRow: View {
    let item: TimelineItem
    let agent: AgentMessage
    var repeats = 1
    var memoryID: String? = nil
    @State private var opened: RowChoice<Bool>?
    @State private var truncated = false
    @Environment(\.transcriptRows) private var rows
    @Environment(\.expandAgentMessages) private var expandAll

    private var rowKey: String { memoryID ?? TranscriptRowID.item(item.id) }
    private var files: [AttachedFile] { item.files ?? [] }
    private var expandable: Bool { AgentRowStyle.expandable(text: item.text, truncated: truncated, files: files.count) }
    private var expanded: Bool { expandAll || (rows.current(opened) ?? rows.isOpen(rowKey) ?? false) }
    private var chip: String { AgentMessage.chip(agentChip(item, agent), repeats: repeats) }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Button(action: toggle) {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Image(systemName: "chevron.right")
                        .font(.caption2.weight(.semibold))
                        .foregroundStyle(PhonePalette.muted)
                        .rotationEffect(.degrees(expanded ? 90 : 0))
                        .opacity(expanded || expandable ? 1 : 0)
                        .accessibilityHidden(true)
                    if expanded {
                        Text(chip).font(.subheadline.weight(.semibold)).foregroundStyle(PhonePalette.peer)
                        Spacer(minLength: 0)
                        if !expandAll { Text("Show less").font(.footnote.weight(.medium)).foregroundStyle(PhonePalette.accentText) }
                    } else {
                        VStack(alignment: .leading, spacing: 2) {
                            ClampedText(text: Text(chip).fontWeight(.semibold).foregroundStyle(PhonePalette.peer) + Text("  ") + Text(AgentMessage.inline(agent.summary)).italic().foregroundStyle(.secondary), lines: AgentRowStyle.previewLines, truncated: $truncated)
                                .font(.subheadline)
                                .multilineTextAlignment(.leading)
                            if expandable {
                                Text(files.isEmpty ? "Show more" : "Show more · \(files.count == 1 ? "1 file" : "\(files.count) files")")
                                    .font(.footnote.weight(.medium))
                                    .foregroundStyle(PhonePalette.accentText)
                            }
                        }
                    }
                }
                .frame(minHeight: 32)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel(expanded ? "From \(chip)" : "\(chip): \(agent.summary)")
            .accessibilityValue(expandable || expanded ? (expanded ? "Expanded" : "Collapsed") : "")
            .accessibilityIdentifier("agent-message-" + item.id)
            if expanded {
                VStack(alignment: .leading, spacing: 8) {
                    if !item.text.isEmpty { SessionMarkdown(text: item.text, textStyle: .subheadline) }
                    ForEach(Array(files.enumerated()), id: \.offset) { _, file in AttachedFileChip(file: file) }
                    if item.truncated { ShortenedNote() }
                    HStack {
                        Spacer(minLength: 0)
                        SessionTimestamp(value: item.timestamp)
                    }
                }
                .padding(.leading, 18)
            }
        }
        .contextMenu {
            Button("Copy", systemImage: "doc.on.doc") { UIPasteboard.general.string = item.text }
        }
    }

    private func toggle() {
        guard !expandAll, expandable || expanded else { return }
        let next = !expanded
        opened = rows.choice(next)
        rows.set(rowKey, open: next)
    }
}

struct IOSAgentSendRuns: View {
    let items: [TimelineItem]

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            ForEach(items.filter { $0.agent?.isSend == true }) { call in
                if let agent = call.agent {
                    IOSAgentSendRow(call: call, agent: agent, result: items.first { $0.kind == "toolResult" && $0.callId != nil && $0.callId == call.callId })
                }
            }
        }
    }
}

struct IOSAgentSendRow: View {
    let call: TimelineItem
    let agent: AgentMessage
    var result: TimelineItem? = nil
    @State private var opened: RowChoice<Bool>?
    @Environment(\.transcriptRows) private var rows
    @Environment(\.expandAgentMessages) private var expandAll

    private var message: String { agent.body ?? "" }
    private var failed: Bool { result.map(ToolRunSummary.isFailure) ?? false }
    private var expanded: Bool { !message.isEmpty && (expandAll || (rows.current(opened) ?? rows.isOpen(TranscriptRowID.item(call.id)) ?? false)) }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Button {
                guard !expandAll, !message.isEmpty else { return }
                let next = !expanded
                opened = rows.choice(next)
                rows.set(TranscriptRowID.item(call.id), open: next)
            } label: {
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Image(systemName: "arrow.turn.down.right").font(.caption).foregroundStyle(PhonePalette.muted)
                    (Text("To \(agent.chip): ").fontWeight(.medium) + Text(AgentMessage.inline(agent.summary)))
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                    if failed { Text("failed").font(.footnote.weight(.medium)).foregroundStyle(PhonePalette.failed) }
                    Spacer(minLength: 0)
                }
                .frame(minHeight: 26)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Sent to \(agent.chip): \(agent.summary)\(failed ? ", failed" : "")")
            .accessibilityValue(message.isEmpty ? "" : expanded ? "Expanded" : "Collapsed")
            .accessibilityIdentifier("agent-send-" + call.id)
            if expanded {
                InlineText(message, parser: .linkedPlain)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .phoneInlineStyle()
                    .textual.textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.leading, 20)
            }
        }
        .padding(.leading, 16)
        .contextMenu {
            if !message.isEmpty { Button("Copy message", systemImage: "doc.on.doc") { UIPasteboard.general.string = message } }
            CopyCallID(item: call)
        }
    }
}

struct IOSAgentFoldRow: View {
    let group: TimelineGroup
    @State private var opened: RowChoice<Bool>?
    @Environment(\.transcriptRows) private var rows

    private var expanded: Bool { rows.current(opened) ?? rows.isOpen(group.id) ?? false }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Button {
                let next = !expanded
                opened = rows.choice(next)
                rows.set(group.id, open: next)
            } label: {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Image(systemName: "chevron.right")
                        .font(.caption2.weight(.semibold))
                        .foregroundStyle(PhonePalette.muted)
                        .rotationEffect(.degrees(expanded ? 90 : 0))
                        .accessibilityHidden(true)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(group.foldLabel ?? "").font(.subheadline.weight(.semibold)).foregroundStyle(PhonePalette.peer)
                        Text(group.foldSenders).font(.footnote).foregroundStyle(.secondary).lineLimit(1)
                    }
                    Spacer(minLength: 0)
                }
                .frame(minHeight: 44)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel(group.foldLabel ?? "")
            .accessibilityValue(expanded ? "Expanded" : "Collapsed")
            .accessibilityHint("From \(group.foldSenders)")
            .accessibilityIdentifier("agent-fold-" + group.firstItemID)
            if expanded {
                VStack(alignment: .leading, spacing: 8) {
                    ForEach(group.items) { item in
                        if let agent = item.agent { IOSAgentMessageRow(item: item, agent: agent, repeats: group.repeatsByItem[item.id] ?? 1) }
                    }
                }
                .padding(.leading, 18)
            }
        }
    }
}
