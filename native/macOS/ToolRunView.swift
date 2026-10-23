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
        let changes = FileChange.merged(entries.flatMap(\.changes))
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
            if !changes.isEmpty {
                FileChips(changes: changes)
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

    private var failed: Bool { results.contains(where: ToolRunSummary.isFailure) || ToolRunSummary.isFailure(call) }

    static func fullInput(_ text: String) -> String {
        guard let object = try? JSONSerialization.jsonObject(with: Data(text.utf8)) as? [String: Any], !object.isEmpty else { return text }
        let command = (object["command"] ?? object["cmd"]) as? String
        let rest = object.filter { $0.key != "command" && $0.key != "cmd" }
        var lines: [String] = command.map { ["$ " + $0] } ?? []
        for key in rest.keys.sorted() {
            let value = rest[key].map { value -> String in
                if let string = value as? String { return string }
                if JSONSerialization.isValidJSONObject(value), let data = try? JSONSerialization.data(withJSONObject: value, options: [.prettyPrinted, .sortedKeys]) {
                    return String(decoding: data, as: UTF8.self)
                }
                return "\(value)"
            } ?? ""
            lines.append("\(key): \(value)")
        }
        return lines.joined(separator: "\n")
    }

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
                        CodeBlock(text: Self.fullInput(call.text), lineLimit: 40)
                        ShortenedNote(item: call)
                    }
                    if results.isEmpty {
                        Text("No result yet")
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

struct FileChips: View {
    let changes: [FileChange]

    var body: some View {
        HStack(spacing: 6) {
            ForEach(changes, id: \.path) { change in
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
                .background(DesktopPalette.bubble, in: RoundedRectangle(cornerRadius: DesktopRadius.control))
                .overlay(RoundedRectangle(cornerRadius: DesktopRadius.control).strokeBorder(DesktopPalette.line))
                .help(change.path)
                .accessibilityElement(children: .ignore)
                .accessibilityLabel("\(change.name), \(change.added) added, \(change.removed) removed")
            }
        }
    }
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
