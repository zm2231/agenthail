import SwiftUI

enum IOSRowAction: Identifiable {
    case rename(SessionState)
    case adopt(SessionState)
    case move(SessionState)

    var id: String {
        switch self {
        case let .rename(session): "rename:" + session.id
        case let .adopt(session): "adopt:" + session.id
        case let .move(session): "move:" + session.id
        }
    }
}

struct IOSRowActions {
    var present: (IOSRowAction) -> Void = { _ in }
    var makeLead: (SessionState) -> Void = { _ in }
}

extension EnvironmentValues {
    @Entry var rowActions = IOSRowActions()
}

struct IOSSessionRowMenu: ViewModifier {
    var model: AgenthailIOSModel
    let session: SessionState
    let enabled: Bool
    @Environment(\.rowActions) private var actions

    func body(content: Content) -> some View {
        if enabled { menu(content) } else { content }
    }

    private func menu(_ content: Content) -> some View {
        content.contextMenu {
            Button("Rename…", systemImage: "pencil") { actions.present(.rename(session)) }
            if session.canAdopt {
                Button("Open in Agenthail", systemImage: "arrow.down.app") { actions.present(.adopt(session)) }
            }
            if model.canMove(session) {
                Button("Move to Another Computer…", systemImage: "arrow.right.circle") { actions.present(.move(session)) }
            }
            Divider()
            IOSPinButton(model: model, session: session)
            Button(session.archived == true ? "Unarchive" : "Archive", systemImage: "archivebox") {
                Task { await model.setArchived(sessionID: session.id, session.archived != true) }
            }
            if model.canAddFolder(session) {
                Button("Add Folder as Project", systemImage: "folder.badge.plus") {
                    Task { model.settle(await model.addFolder(of: session)) }
                }
            }
            if model.canMakeLead(session) {
                Button("Make Project Lead", systemImage: "person.badge.key") { actions.makeLead(session) }
            }
        }
    }
}

struct IOSRowActionHost: ViewModifier {
    var model: AgenthailIOSModel
    @State private var action: IOSRowAction?
    @State private var replacing: (session: SessionState, message: String)?

    func body(content: Content) -> some View {
        content
            .environment(\.rowActions, IOSRowActions(present: { action = $0 }, makeLead: { makeLead($0, replace: false) }))
            .sheet(item: $action) { action in
                switch action {
                case let .rename(session):
                    IOSNameSessionSheet(model: model, sessionID: session.id, alias: session.alias) { self.action = nil }
                case let .adopt(session):
                    IOSAdoptSessionSheet(model: model, session: model.routedSession(session) ?? session, sessionID: session.id)
                case let .move(session):
                    IOSMoveSessionSheet(model: model, session: session) {}
                }
            }
            .alert(replacing?.message ?? "", isPresented: Binding(get: { replacing != nil }, set: { if !$0 { replacing = nil } }), presenting: replacing) { pending in
                Button("Replace", role: .destructive) { makeLead(pending.session, replace: true) }
                Button("Cancel", role: .cancel) {}
            }
    }

    private func makeLead(_ session: SessionState, replace: Bool) {
        Task {
            let change = await model.makeLead(session.id, replace: replace)
            if case .confirmReplace(let message) = change { replacing = (session, message) } else { model.settle(change) }
        }
    }
}

struct IOSPinButton: View {
    var model: AgenthailIOSModel
    let session: SessionState

    var body: some View {
        Button(session.isPinned ? "Unpin" : "Pin", systemImage: session.isPinned ? "pin.slash" : "pin") {
            Task { await model.setPinned(sessionID: session.id, !session.isPinned) }
        }
    }
}
