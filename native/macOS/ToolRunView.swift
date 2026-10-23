import AppKit
import SwiftUI
import Textual

/// A run of tool activity between messages: one quiet line per call, the files it changed as chips, and a
/// single summary line in place of the lines when the run is long.
struct ToolRunView: View {
    let id: String
    let items: [TimelineItem]
    @State private var opened: RowChoice<Bool>?
    @Environment(\.transcriptRows) private var rows
    @Environment(\.holdTranscriptPlace) private var holdPlace

    var body: some View {
        let entries = ToolRunSummary.entries(items)
        let folds = ToolRunSummary.folds(entries)
        let open = !folds || (rows.current(opened) ?? rows.isOpen(id) ?? false)
        let diffs = FileDiff.merged(entries.flatMap(\.diffs))
        VStack(alignment: .leading, spacing: 2) {
            if folds {
                let failed = items.filter(ToolRunSummary.isFailure).count
                Button {
                    holdPlace()
                    opened = rows.choice(!open)
                    rows.set(id, open: !open)
                } label: {
                    ToolLineLabel(verb: ToolRunSummary.label(items), object: "", isCode: false, failed: false, failures: failed, trailing: open ? "chevron.down" : "chevron.right")
                }
                .buttonStyle(.plain)
                .accessibilityValue(open ? "Expanded" : "Collapsed")
            }
            if open {
                ForEach(entries) { entry in
                    switch entry {
                    case .call(let call, let results):
                        ToolLine(call: call, results: results)
                    case .output(let result):
                        OutputLine(result: result)
                    case .reasoning(let item):
                        ReasoningLine(item: item)
                    }
                }
            }
            if !diffs.isEmpty {
                FileChips(run: id, diffs: diffs)
                    .padding(.top, DesktopSpace.xs)
                    .padding(.leading, ToolLineLabel.indent)
            }
        }
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

    static let indent: CGFloat = 16

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 6) {
            Text("◆")
                .accessibilityHidden(true)
                .font(.system(size: 8))
                .foregroundStyle(failed ? DesktopPalette.failed : DesktopPalette.muted)
                .frame(width: ToolLineLabel.indent - 6, alignment: .leading)
            Text(verb)
                .lineLimit(1)
                .layoutPriority(1)
            if !object.isEmpty {
                Text(object)
                    .font(isCode ? .system(size: DesktopType.meta, design: .monospaced) : .system(size: DesktopType.secondary))
                    .foregroundStyle(failed ? DesktopPalette.failed : DesktopPalette.muted)
                    .lineLimit(1)
                    .truncationMode(.tail)
            }
            if failed {
                Text("failed")
                    .font(.system(size: DesktopType.caption))
            } else if failures > 0 {
                Text("\(failures) failed")
                    .font(.system(size: DesktopType.caption))
                    .foregroundStyle(DesktopPalette.failed)
            }
            if let trailing {
                Image(systemName: trailing)
                    .font(.system(size: 8, weight: .semibold))
                    .foregroundStyle(DesktopPalette.muted)
            }
        }
        .font(.system(size: DesktopType.secondary))
        .foregroundStyle(failed ? DesktopPalette.failed : DesktopPalette.text2)
        .frame(maxWidth: .infinity, minHeight: 20, alignment: .leading)
        .contentShape(Rectangle())
    }
}

struct ToolLine: View {
    let call: TimelineItem
    let results: [TimelineItem]
    @State private var opened: RowChoice<Bool>?
    @Environment(\.transcriptRows) private var rows
    @Environment(\.holdTranscriptPlace) private var holdPlace
    @Environment(\.toolCallStates) private var states

    private var failed: Bool { results.contains(where: ToolRunSummary.isFailure) || ToolRunSummary.isFailure(call) }

    var body: some View {
        let presentation = ToolPresentation(name: call.title, text: call.text)
        let object = presentation.object
        // A failure is the one line that opens itself, so its output is on screen without a click.
        let open = rows.current(opened) ?? rows.isOpen(call.id) ?? failed
        VStack(alignment: .leading, spacing: DesktopSpace.xs) {
            Button {
                holdPlace()
                opened = rows.choice(!open)
                rows.set(call.id, open: !open)
            } label: {
                ToolLineLabel(verb: presentation.verb, object: object.text, isCode: object.isCode, failed: failed)
            }
            .buttonStyle(.plain)
            .accessibilityValue(open ? "Expanded" : "Collapsed")
            if open {
                VStack(alignment: .leading, spacing: 6) {
                    if !failed {
                        let diffs = presentation.diffs
                        if diffs.isEmpty {
                            CodeBlock(text: presentation.readableInput, lineLimit: 40)
                        } else {
                            ForEach(diffs, id: \.path) { file in
                                if diffs.count > 1 {
                                    Text(file.change.name)
                                        .font(.system(size: DesktopType.meta, design: .monospaced))
                                        .foregroundStyle(DesktopPalette.muted)
                                }
                                DiffView(id: call.id + ":" + file.path, hunks: file.hunks)
                            }
                        }
                        ShortenedNote(item: call)
                    }
                    if results.isEmpty {
                        Text(states[call.id].label)
                            .font(.system(size: DesktopType.meta))
                            .foregroundStyle(DesktopPalette.muted)
                    }
                    ForEach(results) { result in
                        ToolOutputBlock(result: result)
                    }
                }
                .padding(.leading, ToolLineLabel.indent)
                .padding(.bottom, DesktopSpace.xs)
            }
        }
        .contextMenu {
            Button("Copy input") { copyToPasteboard(call.text) }
            if !results.isEmpty {
                Button("Copy output") { copyToPasteboard(results.map(\.text).joined(separator: "\n\n")) }
            }
            if let id = call.callId, !id.isEmpty {
                Button("Copy call ID") { copyToPasteboard(id) }
            }
        }
    }
}

struct ReasoningLine: View {
    let item: TimelineItem
    @State private var opened: RowChoice<Bool>?
    @Environment(\.transcriptRows) private var rows
    @Environment(\.holdTranscriptPlace) private var holdPlace

    var body: some View {
        let open = rows.current(opened) ?? rows.isOpen(item.id) ?? false
        VStack(alignment: .leading, spacing: DesktopSpace.xs) {
            Button {
                holdPlace()
                opened = rows.choice(!open)
                rows.set(item.id, open: !open)
            } label: {
                ToolLineLabel(verb: "Thought", object: item.text.split(separator: "\n", omittingEmptySubsequences: true).first.map(String.init) ?? "", isCode: false, failed: false)
            }
            .buttonStyle(.plain)
            .accessibilityValue(open ? "Expanded" : "Collapsed")
            if open {
                VStack(alignment: .leading, spacing: DesktopSpace.xs) {
                    StructuredText(markdown: item.text)
                        .font(.system(size: DesktopType.body))
                        .foregroundStyle(DesktopPalette.text2)
                        .textual.textSelection(.enabled)
                    ShortenedNote(item: item)
                }
                .padding(.leading, ToolLineLabel.indent)
                .padding(.bottom, DesktopSpace.xs)
            }
        }
    }
}

/// Output recorded without the call that produced it, such as a result from an earlier page.
struct OutputLine: View {
    let result: TimelineItem
    @State private var opened: RowChoice<Bool>?
    @Environment(\.transcriptRows) private var rows
    @Environment(\.holdTranscriptPlace) private var holdPlace

    var body: some View {
        let failed = ToolRunSummary.isFailure(result)
        let open = rows.current(opened) ?? rows.isOpen(result.id) ?? failed
        VStack(alignment: .leading, spacing: DesktopSpace.xs) {
            Button {
                holdPlace()
                opened = rows.choice(!open)
                rows.set(result.id, open: !open)
            } label: {
                ToolLineLabel(verb: "Output", object: result.text.split(separator: "\n", omittingEmptySubsequences: true).first.map(String.init) ?? "", isCode: true, failed: failed)
            }
            .buttonStyle(.plain)
            .accessibilityValue(open ? "Expanded" : "Collapsed")
            if open {
                ToolOutputBlock(result: result)
                    .padding(.leading, ToolLineLabel.indent)
                    .padding(.bottom, DesktopSpace.xs)
            }
        }
        .contextMenu {
            Button("Copy output") { copyToPasteboard(result.text) }
            if let id = result.callId, !id.isEmpty {
                Button("Copy call ID") { copyToPasteboard(id) }
            }
        }
    }
}

struct ToolOutputBlock: View {
    let result: TimelineItem
    @State private var shownLines: RowChoice<Int>?
    @Environment(\.transcriptRows) private var rows
    @Environment(\.holdTranscriptPlace) private var holdPlace

    /// Lines added per "Show more", so a long output is laid out a page at a time rather than as one text.
    static let page = 500

    var body: some View {
        let failed = ToolRunSummary.isFailure(result)
        let preview = ToolRunSummary.outputPreview(result.text)
        let lines = result.text.split(separator: "\n", omittingEmptySubsequences: false)
        let shown = rows.current(shownLines) ?? rows.count("output-" + result.id) ?? 0
        VStack(alignment: .leading, spacing: DesktopSpace.xs) {
            HStack(spacing: 10) {
                Text(failed ? "Error output" : "Output")
                    .font(.system(size: DesktopType.caption, weight: .semibold))
                    .foregroundStyle(failed ? DesktopPalette.failed : DesktopPalette.muted)
                Spacer()
                if preview.count < result.text.count {
                    if shown < lines.count {
                        Button(shown == 0 ? "Show all \(lines.count) lines" : "Show \(min(Self.page, lines.count - shown)) more lines") { show(shown + Self.page) }
                    }
                    if shown > 0 {
                        Button("Show less") { show(0) }
                    }
                }
                Button("Copy output") { copyToPasteboard(result.text) }
            }
            .font(.system(size: DesktopType.caption))
            .buttonStyle(.link)
            if result.text.isEmpty {
                CodeBlock(text: "The tool returned an empty result.", failed: failed)
            } else if shown == 0 {
                CodeBlock(text: preview, failed: failed)
            } else {
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(Array(stride(from: 0, to: min(shown, lines.count), by: 100)), id: \.self) { start in
                        Text(lines[start..<min(start + 100, shown, lines.count)].joined(separator: "\n"))
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
                .font(.system(size: DesktopType.meta, design: .monospaced))
                .foregroundStyle(failed ? DesktopPalette.failed : DesktopPalette.text)
                .textSelection(.enabled)
                .padding(DesktopSpace.s)
                .background(DesktopPalette.bubble, in: RoundedRectangle(cornerRadius: DesktopRadius.control))
            }
            ShortenedNote(item: result)
        }
    }

    private func show(_ count: Int) {
        holdPlace()
        shownLines = rows.choice(count)
        rows.set("output-" + result.id, count: count)
    }
}

/// The files a run changed. Clicking one shows what changed in it.
struct FileChips: View {
    let run: String
    let diffs: [FileDiff]
    @State private var opened: [String: RowChoice<Bool>] = [:]
    @Environment(\.transcriptRows) private var rows
    @Environment(\.holdTranscriptPlace) private var holdPlace

    var body: some View {
        VStack(alignment: .leading, spacing: DesktopSpace.xs) {
            HStack(spacing: 6) {
                ForEach(diffs, id: \.path) { file in
                    let change = file.change
                    let open = isOpen(file.path)
                    Button {
                        holdPlace()
                        opened[file.path] = rows.choice(!open)
                        rows.set(key(file.path), open: !open)
                    } label: {
                        HStack(spacing: 5) {
                            Text(change.name)
                                .foregroundStyle(DesktopPalette.text)
                            if change.added > 0 {
                                Text("+\(change.added)").foregroundStyle(DesktopPalette.done)
                            }
                            if change.removed > 0 {
                                Text("−\(change.removed)").foregroundStyle(DesktopPalette.failed)
                            }
                        }
                        .font(.system(size: DesktopType.meta, design: .monospaced))
                        .lineLimit(1)
                        .padding(.horizontal, 7)
                        .padding(.vertical, 2)
                        .background(open ? DesktopPalette.line : DesktopPalette.bubble, in: RoundedRectangle(cornerRadius: DesktopRadius.control))
                        .overlay(RoundedRectangle(cornerRadius: DesktopRadius.control).strokeBorder(DesktopPalette.line))
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .help(change.path)
                    .accessibilityLabel("\(change.name), \(change.added) added, \(change.removed) removed")
                    .accessibilityValue(open ? "Expanded" : "Collapsed")
                    .contextMenu {
                        Button("Copy path") { copyToPasteboard(change.path) }
                    }
                }
            }
            ForEach(diffs.filter { isOpen($0.path) }, id: \.path) { file in
                VStack(alignment: .leading, spacing: 2) {
                    if diffs.count > 1 {
                        Text(file.change.name)
                            .font(.system(size: DesktopType.meta, design: .monospaced))
                            .foregroundStyle(DesktopPalette.muted)
                    }
                    DiffView(id: key(file.path), hunks: file.hunks)
                }
            }
        }
    }

    private func key(_ path: String) -> String { "chip-" + run + ":" + path }

    private func isOpen(_ path: String) -> Bool { rows.current(opened[path]) ?? rows.isOpen(key(path)) ?? false }
}

struct CodeBlock: View {
    let text: String
    var failed = false
    var lineLimit: Int? = nil

    var body: some View {
        Text(text)
            .font(.system(size: DesktopType.meta, design: .monospaced))
            .foregroundStyle(failed ? DesktopPalette.failed : DesktopPalette.text)
            .textSelection(.enabled)
            .lineLimit(lineLimit)
            .padding(DesktopSpace.s)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(DesktopPalette.bubble, in: RoundedRectangle(cornerRadius: DesktopRadius.control))
    }
}

struct ShortenedNote: View {
    let item: TimelineItem

    var body: some View {
        if let note = TimelineMetadata.note(item) {
            Text(note)
                .font(.system(size: DesktopType.caption))
                .foregroundStyle(DesktopPalette.muted)
        }
    }
}

func copyToPasteboard(_ text: String) {
    NSPasteboard.general.clearContents()
    NSPasteboard.general.setString(text, forType: .string)
}
