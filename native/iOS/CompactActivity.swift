import SwiftUI

extension TimelineItem {
    /// Something said rather than done: Codex streams its replies as text records, other agents as messages.
    var isSaid: Bool { kind == "message" || kind == "text" }
}

struct TimelineGroup: Identifiable {
    let id: String
    var items: [TimelineItem]
    var foldLabel: String? = nil
    var foldSenders = ""
    var callCount: Int { items.filter { $0.kind == "toolCall" }.count }
    var errorCount: Int { items.filter(ToolRunSummary.isFailure).count }
    var isSend: Bool { items.first?.agent?.isSend == true }
    var isActivity: Bool { foldLabel == nil && !isSend && items.allSatisfy { ["toolCall", "toolResult", "reasoning"].contains($0.kind) } }
    var folds: Bool { callCount > ToolRunSummary.foldAfter }

    /// An agent's reply, with the duration of the turn it ended when the host recorded one.
    var isReply: Bool {
        guard let first = items.first else { return false }
        return first.isSaid && (first.role ?? "assistant") == "assistant" && !first.isInstructions
    }
    var finished: String? { isReply ? items.dropFirst().first(where: Self.isTurnDuration)?.text : nil }

    static func isTurnDuration(_ item: TimelineItem) -> Bool { item.kind == "event" && item.title == "Turn duration" }

    /// Files the run changed, one per file, leaving out calls whose result failed.
    var diffs: [FileDiff] {
        FileDiff.merged(invocations.flatMap { invocation -> [FileDiff] in
            guard let call = invocation.items.first, call.kind == "toolCall",
                  !invocation.items.dropFirst().contains(where: ToolRunSummary.isFailure) else { return [] }
            return ToolPresentation(name: call.title, text: call.text).diffs
        })
    }

    static func make(_ items: [TimelineItem], foldAgents: Bool = true) -> [TimelineGroup] {
        var groups: [TimelineGroup] = []
        var sends: [String: Int] = [:]
        for item in items {
            if item.agent?.isSend == true {
                if groups.last?.isSend != true { groups.append(TimelineGroup(id: item.id, items: [])) }
                groups[groups.count - 1].items.append(item)
                if let call = item.callId { sends[call] = groups.count - 1 }
                continue
            }
            if item.kind == "toolResult", let call = item.callId, let index = sends[call] {
                groups[index].items.append(item)
                continue
            }
            let activity = ["toolCall", "toolResult", "reasoning"].contains(item.kind)
            if activity, groups.last?.isActivity == true { groups[groups.count - 1].items.append(item) }
            else if isTurnDuration(item), let last = groups.last, last.isReply, last.items.count == 1 { groups[groups.count - 1].items.append(item) }
            else { groups.append(TimelineGroup(id: item.id, items: [item])) }
        }
        return foldAgents ? AgentFoldGroups.folded(groups) : groups
    }

    var invocations: [TimelineGroup] {
        var groups: [TimelineGroup] = []
        var calls: [String: Int] = [:]
        for item in items {
            if item.isSaid { calls.removeAll() }
            if item.kind == "toolResult", let callID = item.callId, let index = calls[callID] {
                groups[index].items.append(item)
            } else {
                if item.kind == "toolCall", let callID = item.callId { calls[callID] = groups.count }
                groups.append(TimelineGroup(id: item.id, items: [item]))
            }
        }
        return groups
    }
}

/// A run of tool activity between messages: one quiet line per call, the files it changed as chips, and a
/// single summary line in place of the lines when the run is long.
struct CompactActivityGroup: View {
    let group: TimelineGroup
    var attachmentLoader: ((TimelineItem) async -> Data?)? = nil
    var onInspect: () -> Void = {}
    @State private var opened: RowChoice<Bool>?
    @State private var top: CGFloat = 0
    @Environment(\.transcriptRows) private var rows
    @Environment(\.revealRow) private var revealRow

    var body: some View {
        if group.foldLabel != nil {
            IOSAgentFoldRow(group: group)
        } else if group.isSend {
            IOSAgentSendRuns(items: group.items)
        } else if group.isActivity {
            let open = !group.folds || (rows.current(opened) ?? rows.isOpen(group.id) ?? false)
            let diffs = group.diffs
            VStack(alignment: .leading, spacing: 0) {
                if group.folds {
                    Button { onInspect(); set(!open) } label: {
                        ToolLineLabel(verb: ToolRunSummary.label(group.items), object: "", isCode: false, failed: false, failures: group.errorCount, trailing: open ? "chevron.down" : "chevron.right")
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("activity-" + group.id)
                    .accessibilityValue(open ? "Expanded" : "Collapsed")
                    .accessibilityHint("Show each tool in this run")
                    .contentTop($top)
                }
                if open {
                    ForEach(group.invocations) { invocation in
                        if let call = invocation.items.first, call.kind == "toolCall" {
                            ToolActivityRow(call: call, results: Array(invocation.items.dropFirst()), attachmentLoader: attachmentLoader, onInspect: onInspect)
                        } else {
                            ForEach(invocation.items) { IOSTimelineRow(item: $0, attachmentLoader: attachmentLoader, onInspect: onInspect) }
                        }
                    }
                    if group.folds {
                        Button {
                            // Collapsing from the end removes the rows on screen; bring the run back rather than leave the offset past the content.
                            set(false)
                            revealRow(top)
                        } label: {
                            CollapseLabel(title: "Collapse activity")
                        }
                        .buttonStyle(.plain)
                        .accessibilityIdentifier("collapse-activity-" + group.id)
                    }
                }
                if !diffs.isEmpty {
                    FileChips(run: group.id, diffs: diffs)
                        .padding(.leading, ToolLineLabel.indent)
                        .padding(.vertical, 4)
                }
            }
        } else if group.isReply, let reply = group.items.first {
            IOSTimelineRow(item: reply, finished: group.finished, attachmentLoader: attachmentLoader)
        } else {
            ForEach(group.items) { IOSTimelineRow(item: $0, attachmentLoader: attachmentLoader) }
        }
    }

    private func set(_ open: Bool) {
        opened = rows.choice(open)
        rows.set(group.id, open: open)
    }
}

/// The control at the end of an open run or tool, the full row wide and at least 44 points tall to tap.
struct CollapseLabel: View {
    let title: String

    var body: some View {
        Label(title, systemImage: "chevron.up")
            .font(.footnote.weight(.medium))
            .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
            .contentShape(Rectangle())
    }
}

/// "◆ Run swift test": the shape every tool, reasoning and output line shares.
struct ToolLineLabel: View {
    let verb: String
    let object: String
    let isCode: Bool
    let failed: Bool
    var failures = 0
    var trailing: String? = nil
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    @ScaledMetric(relativeTo: .subheadline) private var markWidth = Self.indent - 6

    static let indent: CGFloat = 18

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 6) {
            // Set in the line's own font and scaled about its centre, the mark stays on the middle of the
            // first line at every text size; a fixed small font would sit on the baseline.
            Text("◆")
                .font(.subheadline)
                .fixedSize()
                .scaleEffect(0.6)
                .foregroundStyle(failed ? AnyShapeStyle(PhonePalette.failed) : AnyShapeStyle(PhonePalette.muted))
                .frame(width: markWidth, alignment: .leading)
                .accessibilityHidden(true)
            Text(verb)
                .font(.subheadline)
                .foregroundStyle(failed ? AnyShapeStyle(PhonePalette.failed) : AnyShapeStyle(.primary))
                .lineLimit(dynamicTypeSize.isAccessibilitySize ? 2 : 1)
                .layoutPriority(1)
            if !object.isEmpty {
                Text(object)
                    .font(isCode ? .footnote.monospaced() : .subheadline)
                    .foregroundStyle(failed ? AnyShapeStyle(PhonePalette.failed) : AnyShapeStyle(PhonePalette.muted))
                    .lineLimit(dynamicTypeSize.isAccessibilitySize ? 3 : 1)
                    .truncationMode(.tail)
            }
            if failed {
                Text("failed").font(.caption).foregroundStyle(PhonePalette.failed)
            } else if failures > 0 {
                Text("\(failures) failed").font(.caption).foregroundStyle(PhonePalette.failed)
            }
            Spacer(minLength: 0)
            if let trailing {
                Image(systemName: trailing).font(.caption2.weight(.semibold)).foregroundStyle(PhonePalette.muted)
            }
        }
        .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
        .contentShape(Rectangle())
    }
}

struct ToolActivityRow: View {
    let call: TimelineItem
    let results: [TimelineItem]
    var attachmentLoader: ((TimelineItem) async -> Data?)? = nil
    var standaloneRecord = false
    var onInspect: () -> Void = {}
    @State private var opened: RowChoice<Bool>?
    @State private var top: CGFloat = 0
    @Environment(\.transcriptRows) private var rows
    @Environment(\.revealRow) private var revealRow
    @Environment(\.toolCallStates) private var states

    private var presentation: ToolPresentation { ToolPresentation(name: call.title, text: call.text) }
    private var failed: Bool { results.contains(where: ToolRunSummary.isFailure) || ToolRunSummary.isFailure(call) }

    var body: some View {
        let object = presentation.object
        // A failure is the one line that opens itself, so its output is on screen without a tap.
        let open = rows.current(opened) ?? rows.isOpen(call.id) ?? failed
        VStack(alignment: .leading, spacing: 0) {
            Button { onInspect(); set(!open) } label: {
                ToolLineLabel(verb: presentation.verb, object: object.text, isCode: object.isCode, failed: failed, trailing: open ? "chevron.down" : "chevron.right")
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("tool-" + call.id)
            .accessibilityValue(open ? "Expanded" : "Collapsed")
            .accessibilityHint("Show recorded input and output")
            .contextMenu {
                Button("Copy input", systemImage: "doc.on.doc") { UIPasteboard.general.string = call.text }
                if !results.isEmpty {
                    Button("Copy output", systemImage: "doc.on.doc") { UIPasteboard.general.string = results.map(\.text).joined(separator: "\n\n") }
                }
                CopyCallID(item: call)
            }
            .contentTop($top)
            if open {
                VStack(alignment: .leading, spacing: 12) {
                    if !failed {
                        ToolContentView(item: call)
                        if call.truncated { ShortenedNote() }
                    }
                    if results.isEmpty && !standaloneRecord {
                        Text(states[call.id].label).font(.footnote).foregroundStyle(PhonePalette.muted)
                    }
                    ForEach(results) { result in
                        if result.kind == "attachment" {
                            AttachmentRow(item: result, loader: attachmentLoader.map { loader in { await loader(result) } })
                        } else {
                            ToolOutput(result: result)
                        }
                    }
                    Button {
                        set(false)
                        revealRow(top)
                    } label: {
                        CollapseLabel(title: "Collapse tool")
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("collapse-tool-" + call.id)
                }
                .padding(.leading, ToolLineLabel.indent)
            }
        }
    }

    private func set(_ open: Bool) {
        opened = rows.choice(open)
        rows.set(call.id, open: open)
    }
}

/// A tool's recorded output: a preview, then the rest a page at a time so a long log is never laid out at once.
struct ToolOutput: View {
    let result: TimelineItem
    @State private var shownLines: RowChoice<Int>?
    @Environment(\.transcriptRows) private var rows

    static let page = 500
    static let chunk = 100

    var body: some View {
        let failed = ToolRunSummary.isFailure(result)
        let preview = ToolRunSummary.outputPreview(result.text)
        let lines = result.text.split(separator: "\n", omittingEmptySubsequences: false)
        let shown = rows.current(shownLines) ?? rows.count("output-" + result.id) ?? 0
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text(failed ? "Error output" : "Output").font(.caption.weight(.semibold))
                Spacer()
                SessionTimestamp(value: result.timestamp)
            }
            .foregroundStyle(failed ? AnyShapeStyle(PhonePalette.failed) : AnyShapeStyle(PhonePalette.muted))
            if result.text.isEmpty {
                TranscriptCode(text: "The tool returned an empty result.")
            } else if shown == 0 {
                TranscriptCode(text: preview)
            } else {
                ScrollView(.horizontal) {
                    VStack(alignment: .leading, spacing: 0) {
                        ForEach(Array(stride(from: 0, to: min(shown, lines.count), by: Self.chunk)), id: \.self) { start in
                            Text(lines[start..<min(start + Self.chunk, shown, lines.count)].joined(separator: "\n"))
                                .fixedSize(horizontal: true, vertical: false)
                        }
                    }
                    .font(.system(.callout, design: .monospaced))
                    .textSelection(.enabled)
                    .padding(12)
                }
                .background(SessionStyle.surface, in: RoundedRectangle(cornerRadius: 10))
            }
            if result.truncated { ShortenedNote() }
            HStack(spacing: 16) {
                if preview.count < result.text.count {
                    if shown < lines.count {
                        Button(shown == 0 ? "Show all \(lines.count) lines" : "Show \(min(Self.page, lines.count - shown)) more lines") { show(shown + Self.page) }
                    }
                    if shown > 0 {
                        Button("Show less") { show(0) }
                    }
                }
                Button("Copy output", systemImage: "doc.on.doc") { UIPasteboard.general.string = result.text }
            }
            .font(.footnote)
            .frame(minHeight: 44)
        }
    }

    private func show(_ count: Int) {
        shownLines = rows.choice(count)
        rows.set("output-" + result.id, count: count)
    }
}

/// The files a run changed. Tapping one shows what changed in it.
struct FileChips: View {
    let run: String
    let diffs: [FileDiff]
    @State private var opened: [String: RowChoice<Bool>] = [:]
    @Environment(\.transcriptRows) private var rows

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            ScrollView(.horizontal) {
                HStack(spacing: 6) {
                    ForEach(diffs, id: \.path) { file in
                        let change = file.change
                        let open = isOpen(file.path)
                        Button {
                            opened[file.path] = rows.choice(!open)
                            rows.set(key(file.path), open: !open)
                        } label: {
                            HStack(spacing: 5) {
                                Text(change.name).foregroundStyle(.primary)
                                if change.added > 0 { Text("+\(change.added)").foregroundStyle(PhonePalette.done) }
                                if change.removed > 0 { Text("−\(change.removed)").foregroundStyle(PhonePalette.diffRemoved) }
                            }
                            .font(.caption.monospaced())
                            .lineLimit(1)
                            .padding(.horizontal, 8)
                            .padding(.vertical, 4)
                            .background(open ? Color(uiColor: .tertiarySystemFill) : SessionStyle.surface, in: RoundedRectangle(cornerRadius: 8))
                            .frame(minHeight: 44)
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel("\(change.name), \(change.added) added, \(change.removed) removed")
                        .accessibilityValue(open ? "Expanded" : "Collapsed")
                        .accessibilityHint("Show what changed in this file")
                        .accessibilityIdentifier("file-chip-" + change.name)
                        .contextMenu {
                            Button("Copy path", systemImage: "doc.on.doc") { UIPasteboard.general.string = change.path }
                        }
                    }
                }
            }
            .scrollIndicators(.hidden)
            .accessibilityIdentifier("file-chips")
            ForEach(diffs.filter { isOpen($0.path) }, id: \.path) { file in
                VStack(alignment: .leading, spacing: 4) {
                    if diffs.count > 1 {
                        Text(file.change.name).font(.caption.monospaced()).foregroundStyle(PhonePalette.muted)
                    }
                    DiffView(id: key(file.path), hunks: file.hunks)
                }
            }
        }
    }

    private func key(_ path: String) -> String { "chip-" + run + ":" + path }

    private func isOpen(_ path: String) -> Bool { rows.current(opened[path]) ?? rows.isOpen(key(path)) ?? false }
}

/// A line between transcript entries for something that happened rather than something said.
struct TranscriptDivider: View {
    let text: String

    static func fits(_ detail: String) -> Bool {
        detail.utf8.count <= 160 && !detail.contains("\n")
    }

    var body: some View {
        HStack(spacing: 10) {
            Rectangle().fill(.separator).frame(minWidth: 16, maxHeight: 0.5)
            Text(text)
                .font(.caption)
                .foregroundStyle(PhonePalette.muted)
                .multilineTextAlignment(.center)
                .layoutPriority(1)
                .textSelection(.enabled)
            Rectangle().fill(.separator).frame(minWidth: 16, maxHeight: 0.5)
        }
        .padding(.vertical, 6)
        .accessibilityElement(children: .combine)
    }
}

struct ShortenedNote: View {
    var body: some View {
        Text(TimelineMetadata.shortened).font(.caption).foregroundStyle(PhonePalette.muted)
    }
}
