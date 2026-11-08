import SwiftUI

struct SidebarTiles: View {
    var model: AgenthailModel
    @ObservedObject var pane: SessionPane

    var body: some View {
        let count = model.decisionFeed.count
        HStack(spacing: DesktopSpace.xs) {
            tile("Inbox", systemImage: "tray", count: count, selected: pane.showsInbox, id: "sidebar-tile-inbox", action: pane.showInbox)
            tile("All", systemImage: "square.stack", count: 0, selected: !pane.showsInbox, id: "sidebar-tile-all", action: pane.showAll)
        }
        .padding(.horizontal, DesktopSpace.s)
        .padding(.bottom, DesktopSpace.xs)
        .task(id: model.decisionsKey) { await model.refreshDecisionsSoon() }
    }

    private func tile(_ title: String, systemImage: String, count: Int, selected: Bool, id: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 6) {
                Image(systemName: systemImage)
                Text(title)
                if count > 0 {
                    Text("\(count)")
                        .font(.system(size: DesktopType.caption, weight: .semibold))
                        .monospacedDigit()
                        .foregroundStyle(DesktopPalette.attention)
                }
            }
            .font(.system(size: DesktopType.body, weight: .medium))
            .foregroundStyle(DesktopPalette.text)
            .frame(maxWidth: .infinity)
            .padding(.vertical, 6)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .selectionMark(selected)
        .accessibilityIdentifier(id)
        .accessibilityValue(count > 0 ? "\(count) waiting" : "")
    }
}
