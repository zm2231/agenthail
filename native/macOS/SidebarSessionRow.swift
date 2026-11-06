import AppKit
import SwiftUI

struct SidebarSessionRow: View, @MainActor Equatable {
    let state: SidebarRowState
    let actions: SidebarRowActions
    @Environment(\.openWindow) private var openWindow

    static func == (lhs: Self, rhs: Self) -> Bool {
        lhs.state == rhs.state && lhs.actions.model === rhs.actions.model && lhs.actions.pane === rhs.actions.pane
    }

    var body: some View {
        let session = state.session
        Button {
            actions.select(keep: NSEvent.modifierFlags.contains(.command))
        } label: {
            SessionRowView(session: session, title: state.title, family: state.family, needsYou: state.needsYou, reason: state.reason, finishedUnseen: state.finishedUnseen)
                .padding(.vertical, DesktopSpace.xs)
                .padding(.horizontal, DesktopSpace.s)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(DesktopPalette.attentionBackground.when(state.needsYou && !state.selected), in: RoundedRectangle(cornerRadius: DesktopRadius.control))
                .selectionMark(state.selected, bar: true)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .simultaneousGesture(TapGesture(count: 2).onEnded { actions.keepTab() })
        .accessibilityAddTraits(state.selected ? .isSelected : [])
        .contextMenu {
            Button("Open in New Window") { openWindow(id: "session", value: session.id) }
            Button("Rename…") { actions.rename() }
            if session.canAdopt {
                Button("Open in Agenthail") { actions.adopt() }
            }
            Divider()
            Button(session.archived == true ? "Unarchive" : "Archive") { actions.toggleArchived() }
            if ProjectMenu.canAddFolder(session), session.cwd != nil {
                Button("Add Folder as Project") { actions.addFolder() }
            }
            if state.canMakeLead {
                Button("Make Project Lead") { actions.makeLead() }
            }
        }
    }
}
