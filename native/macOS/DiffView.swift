import SwiftUI

/// A file's changes as a unified diff: removed lines red, added lines green, and the words that changed marked.
struct DiffView: View {
    let id: String
    let hunks: [TextDiff]
    @State private var shownRows: RowChoice<Int>?
    @Environment(\.transcriptRows) private var rows
    @Environment(\.holdTranscriptPlace) private var holdPlace

    /// Rows laid out per "Show more", so a long diff is built a page at a time.
    static let page = 400

    var body: some View {
        let entries = DiffEntry.entries(hunks)
        let shown = max(rows.current(shownRows) ?? rows.count("diff-" + id) ?? Self.page, Self.page)
        VStack(alignment: .leading, spacing: 0) {
            ForEach(entries.prefix(shown)) { entry in
                switch entry.content {
                case .line(let line):
                    DiffLine(line: line)
                case .folded(let count):
                    DiffNote(text: count == 1 ? "1 unchanged line" : "\(count) unchanged lines")
                case .hunk:
                    DiffNote(text: "⋯")
                }
            }
            if entries.count > shown {
                Button("Show \(min(Self.page, entries.count - shown)) more lines") { show(shown + Self.page) }
                    .buttonStyle(.link)
                    .font(.system(size: DesktopType.caption))
                    .padding(DesktopSpace.s)
            }
        }
        .font(.system(size: DesktopType.meta, design: .monospaced))
        .textSelection(.enabled)
        .padding(.vertical, DesktopSpace.xs)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(DesktopPalette.bubble, in: RoundedRectangle(cornerRadius: DesktopRadius.control))
        .clipShape(RoundedRectangle(cornerRadius: DesktopRadius.control))
    }

    private func show(_ count: Int) {
        holdPlace()
        shownRows = rows.choice(count)
        rows.set("diff-" + id, count: count)
    }
}

private struct DiffLine: View {
    let line: TextDiff.Line

    var body: some View {
        let color: Color? = switch line.kind {
        case .added: DesktopPalette.done
        case .removed: DesktopPalette.failed
        case .same: nil
        }
        HStack(alignment: .firstTextBaseline, spacing: 6) {
            Text(line.kind.sign)
                .foregroundStyle(color ?? DesktopPalette.muted)
                .accessibilityHidden(true)
            Text(line.attributed(marking: color?.opacity(0.32)))
                .foregroundStyle(DesktopPalette.text)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.horizontal, DesktopSpace.s)
        .background(color?.opacity(0.1) ?? .clear)
        .accessibilityElement(children: .combine)
        .accessibilityLabel(line.kind.spoken + line.text)
    }
}

private struct DiffNote: View {
    let text: String

    var body: some View {
        Text(text)
            .foregroundStyle(DesktopPalette.muted)
            .padding(.horizontal, DesktopSpace.s + 16)
            .padding(.vertical, 2)
    }
}
