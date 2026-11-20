import SwiftUI

struct StatusIndicator: View {
    let session: SessionState
    let needsYou: Bool
    var finishedUnseen = false

    var body: some View {
        let kind = SessionDisplayKind.of(session)
        Group {
            if kind == .working && !needsYou {
                WorkingSpinner()
            } else if kind == .finishing && !needsYou {
                Circle()
                    .strokeBorder(DesktopPalette.working, lineWidth: 1.5)
                    .frame(width: 7, height: 7)
            } else {
                Circle()
                    .fill(DesktopPalette.statusColor(session, needsYou: needsYou, finishedUnseen: finishedUnseen))
                    .frame(width: 7, height: 7)
            }
        }
        .frame(width: 12, height: 12)
        .accessibilityHidden(true)
    }
}
