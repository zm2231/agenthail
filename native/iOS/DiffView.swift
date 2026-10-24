import SwiftUI

/// A file's changes as a unified diff: removed lines red, added lines green, and the words that changed marked.
struct DiffView: View {
    let id: String
    let hunks: [TextDiff]
    @State private var shownRows: RowChoice<Int>?
    @Environment(\.transcriptRows) private var rows

    /// Rows laid out per "Show more", so a long diff is built a page at a time.
    static let page = 300

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
                    .font(.footnote)
                    .frame(minHeight: 44)
                    .padding(.horizontal, 12)
            }
        }
        .font(.system(.footnote, design: .monospaced))
        .textSelection(.enabled)
        .padding(.vertical, 6)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(SessionStyle.surface, in: RoundedRectangle(cornerRadius: 10))
        .clipShape(RoundedRectangle(cornerRadius: 10))
        .accessibilityIdentifier("diff-" + id)
    }

    private func show(_ count: Int) {
        shownRows = rows.choice(count)
        rows.set("diff-" + id, count: count)
    }
}

private struct DiffLine: View {
    let line: TextDiff.Line
    @Environment(\.self) private var environment

    var body: some View {
        let style: RoleStyle? = switch line.kind {
        case .added: PhonePalette.done
        case .removed: PhonePalette.diffRemoved
        case .same: nil
        }
        let color = style?.resolve(in: environment)
        HStack(alignment: .firstTextBaseline, spacing: 6) {
            Text(line.kind.sign)
                .foregroundStyle(style ?? PhonePalette.muted)
                .accessibilityHidden(true)
            Text(line.attributed(marking: color?.opacity(0.3)))
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 1)
        .background(color?.opacity(0.1) ?? .clear)
        .accessibilityElement(children: .combine)
        .accessibilityLabel(line.kind.spoken + line.text)
    }
}

private struct DiffNote: View {
    let text: String

    var body: some View {
        Text(text)
            .foregroundStyle(PhonePalette.muted)
            .padding(.horizontal, 26)
            .padding(.vertical, 2)
    }
}
