import SwiftUI

// Reads the catalog cursor in a view of its own, so each catalog event re-runs only this view.
struct DecisionsRefresh: View {
    let model: AgenthailModel

    var body: some View {
        Color.clear.task(id: model.decisionsKey) { await model.refreshDecisionsSoon() }
    }
}
