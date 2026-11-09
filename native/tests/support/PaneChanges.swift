import Observation

@MainActor
final class PaneChanges {
    private(set) var count = 0
    private let pane: SessionPane
    private let read: @MainActor (SessionPane) -> Void

    init(_ pane: SessionPane, reading read: @escaping @MainActor (SessionPane) -> Void = PaneChanges.readAll) {
        self.pane = pane
        self.read = read
        arm()
    }

    private func arm() {
        withObservationTracking { read(pane) } onChange: { [weak self] in
            MainActor.assumeIsolated {
                self?.count += 1
                self?.arm()
            }
        }
    }

    static let trackedCount = 36

    static func readAll(_ p: SessionPane) {
        _ = (p.selectedSessionID, p.tabs, p.newSession, p.shownProjectID, p.archivedTab, p.archivedTabError, p.detail)
        _ = (p.detailStale, p.detailRefreshFailed, p.removedSession, p.olderItems, p.olderCursor, p.loadingOlder, p.olderError)
        _ = (p.inspectorVisible, p.inspectorDocks, p.inspectorOverlay, p.sidebarDocks, p.sidebarOverlay)
        _ = (p.renamingSession, p.adoptingSession, p.projectSheet, p.messagingProject, p.projectConfirmation, p.forking, p.controlPending)
        _ = (p.requests, p.answeringRequest, p.retiredAsks, p.requestError, p.composerDraft, p.draftIsEmpty)
        _ = (p.detailLoadError, p.modelCatalogNeeded, p.scope, p.selectedDecisionID)
    }

    /// Tracked properties are stored behind an underscored backing field; ignored ones keep their names.
    static func trackedFields(of pane: SessionPane) -> Int {
        Mirror(reflecting: pane).children.filter { $0.label?.hasPrefix("_") == true && $0.label != "_$observationRegistrar" }.count
    }
}
