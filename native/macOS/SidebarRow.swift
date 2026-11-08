import Foundation

struct SidebarRowState: Equatable {
    let session: SessionState
    let title: String
    var family: SessionFamily? = nil
    var attention: SessionAttention? = nil
    var finishedUnseen = false
    var selected = false
    var canMakeLead = false

    var needsYou: Bool { attention != nil }
}

@MainActor
struct SidebarRowActions {
    let shown: SessionState
    let model: AgenthailModel
    let pane: SessionPane

    private var current: SessionState { model.session(shown.id) ?? shown }

    func select(keep: Bool) { pane.select(shown.id, keep: keep) }
    func keepTab() { pane.keepTab(shown.id) }
    func rename() { pane.renamingSession = current }
    func adopt() { pane.adoptingSession = current }
    func toggleArchived() { model.setArchived(sessionID: shown.id, current.archived != true) }

    func addFolder() {
        let session = current
        guard let cwd = session.cwd else { return }
        let model = model
        pane.changeProject { _ in await model.addProject(path: cwd, host: ProjectMenu.host(of: session)) }
    }

    func makeLead() {
        let model = model
        let id = shown.id
        pane.changeProject { await model.makeLead(id, replace: $0) }
    }
}
