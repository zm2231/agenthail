import SwiftUI

struct SessionWindow: View {
    var model: AgenthailModel
    @State private var pane: SessionPane
    let sessionID: String

    init(model: AgenthailModel, sessionID: String) {
        self.model = model
        self.sessionID = sessionID
        _pane = State(initialValue: SessionPane(model: model, restoresSelection: false))
    }

    var body: some View {
        @Bindable var pane = pane
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
            .sheet(item: $pane.movingSession) { MoveSessionSheet(model: model, session: $0) }
            .environment(model)
            .navigationTitle(pane.displayedSession?.title ?? "Session")
            .focusedSceneValue(pane)
            .onAppear {
                model.attach(pane)
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
