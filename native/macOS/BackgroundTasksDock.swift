import SwiftUI

struct BackgroundTasksDock: View {
    let sessionID: String
    let background: BackgroundTasks
    @AppStorage(BackgroundDockChoices.dismissedKey) private var dismissed = ""
    @AppStorage(BackgroundDockChoices.openKey) private var openChoices = ""

    var body: some View {
        if BackgroundDockChoices.shows(background, for: sessionID, dismissed: dismissed) {
            dock(open: BackgroundDockChoices.isOpen(background, for: sessionID, choices: openChoices))
        }
    }

    private func dock(open: Bool) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: DesktopSpace.s) {
                Button {
                    openChoices = BackgroundDockChoices.toggling(background, for: sessionID, in: openChoices)
                } label: { header(open: open) }
                    .buttonStyle(.plain)
                    .accessibilityValue(open ? "Expanded" : "Collapsed")
                    .accessibilityIdentifier("background-tasks-toggle")
                Button {
                    dismissed = BackgroundDockChoices.dismissing(background, for: sessionID, in: dismissed)
                } label: {
                    Image(systemName: "xmark")
                        .font(.system(size: DesktopType.caption))
                }
                .buttonStyle(.borderless)
                .foregroundStyle(DesktopPalette.text2)
                .help("Hide until the background tasks change")
                .accessibilityLabel("Hide until the background tasks change")
                .accessibilityIdentifier("background-tasks-dismiss")
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

    private func header(open: Bool) -> some View {
        HStack(spacing: DesktopSpace.s) {
            Image(systemName: "gearshape.2")
                .foregroundStyle(DesktopPalette.text2)
            Text(background.summary)
                .fontWeight(.medium)
                .foregroundStyle(DesktopPalette.text)
            Spacer(minLength: 4)
            Image(systemName: open ? "chevron.up" : "chevron.down")
                .font(.system(size: DesktopType.caption))
                .foregroundStyle(DesktopPalette.text2)
        }
        .contentShape(Rectangle())
    }
}
