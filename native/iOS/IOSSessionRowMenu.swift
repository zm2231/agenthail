import SwiftUI

struct IOSSessionRowMenu: ViewModifier {
    var model: AgenthailIOSModel
    let session: SessionState
    let enabled: Bool
    @State private var renaming = false
    @State private var adopting = false
    @State private var replacing: String?

    func body(content: Content) -> some View {
        if enabled { menu(content) } else { content }
    }

    private func menu(_ content: Content) -> some View {
        content
            .contextMenu {
                Button("Rename…", systemImage: "pencil") { renaming = true }
                if session.canAdopt {
                    Button("Open in Agenthail", systemImage: "arrow.down.app") { adopting = true }
                }
                Divider()
                Button(session.archived == true ? "Unarchive" : "Archive", systemImage: "archivebox") {
                    Task { await model.setArchived(sessionID: session.id, session.archived != true) }
                }
                if model.canAddFolder(session) {
                    Button("Add Folder as Project", systemImage: "folder.badge.plus") {
                        Task { model.settle(await model.addFolder(of: session)) }
                    }
                }
                if model.canMakeLead(session) {
                    Button("Make Project Lead", systemImage: "person.badge.key") { makeLead(replace: false) }
                }
            }
            .sheet(isPresented: $renaming) {
                IOSNameSessionSheet(model: model, sessionID: session.id, alias: session.alias) { renaming = false }
            }
            .sheet(isPresented: $adopting) {
                IOSAdoptSessionSheet(model: model, session: model.routedSession(session) ?? session, sessionID: session.id)
            }
            .alert(replacing ?? "", isPresented: Binding(get: { replacing != nil }, set: { if !$0 { replacing = nil } })) {
                Button("Replace", role: .destructive) { makeLead(replace: true) }
                Button("Cancel", role: .cancel) {}
            }
    }

    private func makeLead(replace: Bool) {
        Task {
            let change = await model.makeLead(session.id, replace: replace)
            if case .confirmReplace(let message) = change { replacing = message } else { model.settle(change) }
        }
    }
}
