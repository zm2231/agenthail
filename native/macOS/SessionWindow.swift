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
        ConversationPane(model: model, pane: pane, headerInset: 84)
            .ignoresSafeArea(.container, edges: .top)
            .inspector(isPresented: $pane.inspectorVisible) {
                SessionInspector(model: model, pane: pane)
                    .ignoresSafeArea(.container, edges: .top)
                    .inspectorColumnWidth(min: 270, ideal: 284, max: 300)
            }
            .background(DesktopPalette.window)
            .sheet(item: $pane.renamingSession) { RenameSessionSheet(model: model, session: $0) }
            .environmentObject(model)
            .navigationTitle(pane.displayedSession?.title ?? "Session")
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
