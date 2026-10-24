import SwiftUI

struct SessionWindow: View {
    @ObservedObject var model: AgenthailModel
    @StateObject private var pane: SessionPane
    let sessionID: String

    init(model: AgenthailModel, sessionID: String) {
        self.model = model
        self.sessionID = sessionID
        _pane = StateObject(wrappedValue: model.openPane())
    }

    var body: some View {
        ConversationPane(model: model, pane: pane, gaps: pane.inspectorVisible ? .trailing : [], holdsWindowButtons: true)
            .paneFrame(pane.inspectorVisible ? [.leading, .vertical] : .all)
            .ignoresSafeArea(.container, edges: .top)
            .inspector(isPresented: $pane.inspectorVisible) {
                SessionInspector(model: model, pane: pane)
                    .paneFrame([.trailing, .vertical])
                    .ignoresSafeArea(.container, edges: .top)
                    .inspectorColumnWidth(min: 270, ideal: 284, max: 300)
            }
            .windowBackdrop(model.interface)
            .sheet(item: $pane.renamingSession) { RenameSessionSheet(model: model, session: $0) }
            .environmentObject(model)
            .navigationTitle(pane.displayedSession?.title ?? "Session")
            .focusedSceneObject(pane)
            .onAppear {
                pane.inspectorVisible = false
                pane.select(sessionID)
                model.windowAppeared()
            }
            .onDisappear {
                model.closePane(pane)
                model.windowDisappeared()
            }
    }
}
