import SwiftUI
import Textual

struct AuditEventRow: View {
    let entry: HistoryState
    @State private var expanded = false

    var body: some View {
        let failed = AuditFacts.isError(entry)
        VStack(alignment: .leading, spacing: 3) {
            HStack {
                Circle()
                    .fill(failed ? DesktopPalette.failed : DesktopPalette.text2)
                    .frame(width: 6, height: 6)
                Text(AuditFacts.kindLabel(entry.kind))
                Spacer()
                Text(relativeAge(entry.createdAt))
                    .font(.system(size: DesktopType.caption))
                    .foregroundStyle(DesktopPalette.text2)
                    .help(entry.createdAt)
            }
            route
                .font(.system(size: DesktopType.secondary))
                .foregroundStyle(DesktopPalette.text2)
                .textSelection(.enabled)
            Button { expanded.toggle() } label: {
                Label("View details", systemImage: expanded ? "chevron.down" : "chevron.right")
                    .font(.system(size: DesktopType.secondary))
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundStyle(DesktopPalette.accentText)
            .accessibilityValue(expanded ? "Expanded" : "Collapsed")
            if expanded {
                StructuredText(markdown: AuditFacts.content(entry))
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(failed ? DesktopPalette.failed : DesktopPalette.text)
                    .textual.textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.top, DesktopSpace.xs)
            }
        }
    }

    @ViewBuilder private var route: some View {
        if let source = AuditFacts.source(entry) {
            Text("\(source) → \(AuditFacts.target(entry))")
        } else {
            Text(AuditFacts.target(entry))
        }
    }
}
