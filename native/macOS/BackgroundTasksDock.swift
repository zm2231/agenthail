import SwiftUI

struct BackgroundTasksDock: View {
    let background: BackgroundTasks
    @State private var expanded = false

    var body: some View {
        let compact = background.isAmbientOnly
        let open = !compact || expanded
        VStack(alignment: .leading, spacing: 4) {
            if compact {
                Button { expanded.toggle() } label: { header(compact: true) }
                    .buttonStyle(.plain)
                    .accessibilityValue(expanded ? "Expanded" : "Collapsed")
            } else {
                header(compact: false)
            }
            if open {
                TimelineView(.everyMinute) { context in
                    VStack(alignment: .leading, spacing: 2) {
                        ForEach(background.tasks ?? []) { task in
                            Text(task.timedLine(now: context.date))
                                .lineLimit(1)
                                .truncationMode(.middle)
                                .help(task.timedLine(now: context.date))
                        }
                        if let note = background.sourceNote {
                            Text(note).foregroundStyle(DesktopPalette.text2)
                        }
                    }
                    .font(.system(size: DesktopType.caption))
                    .foregroundStyle(DesktopPalette.text2)
                    .padding(.leading, 22)
                }
            }
        }
        .font(.system(size: DesktopType.body))
        .padding(.horizontal, 14)
        .padding(.vertical, 7)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(DesktopPalette.dock, in: RoundedRectangle(cornerRadius: DesktopRadius.card))
        .overlay(RoundedRectangle(cornerRadius: DesktopRadius.card).strokeBorder(DesktopPalette.line))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("background-tasks")
    }

    private func header(compact: Bool) -> some View {
        HStack(spacing: DesktopSpace.s) {
            Image(systemName: "gearshape.2")
                .foregroundStyle(DesktopPalette.text2)
            Text(background.summary)
                .fontWeight(.medium)
                .foregroundStyle(DesktopPalette.text)
            Spacer(minLength: 4)
            if compact {
                Image(systemName: expanded ? "chevron.up" : "chevron.down")
                    .font(.system(size: DesktopType.caption))
                    .foregroundStyle(DesktopPalette.text2)
            }
        }
        .contentShape(Rectangle())
    }
}
