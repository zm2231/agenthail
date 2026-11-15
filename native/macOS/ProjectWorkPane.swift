import SwiftUI

struct ProjectWorkPane: View {
    var model: AgenthailModel
    let project: ProjectState

    var body: some View {
        let here = model.daemon?.host?.id ?? ""
        let place = ProjectWork.place(project, preferring: here)
        let catalog = model.extensionCatalogs.first { $0.hostID == place.hostID }
        Group {
            if place.hostID != here {
                unavailable("This project's work list is on \(catalog?.hostName ?? "another computer"). Open it in Agenthail there; this Mac shows only its own.", retry: nil)
            } else if let catalog, let api = catalog.api {
                switch catalog.list == nil && catalog.error != nil ? .unavailable(catalog.error ?? "") : ProjectWork.page(catalog.list, projectID: place.projectID, origin: api.origin, hostName: catalog.hostName) {
                case .reading:
                    ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
                case .shown(let url):
                    let controller = ExtensionPages.controller(id: ProjectWork.controllerID(place), hostID: place.hostID)
                    ExtensionPageHost(controller: controller, url: url, signIn: catalog.signIn)
                        .overlay {
                            if case .failed(let message) = controller.phase {
                                unavailable(message) { controller.reload() }
                            }
                        }
                        .accessibilityIdentifier("project-work-page")
                case .unavailable(let message):
                    unavailable(message) { catalog.refresh() }
                }
            } else {
                unavailable("This Mac can't reach the computer that holds this project's work list.", retry: nil)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .task(id: place) {
            if let catalog, catalog.list == nil, !catalog.loading { catalog.refresh() }
        }
    }

    private func unavailable(_ message: String, retry: (() -> Void)?) -> some View {
        ContentUnavailableView {
            Label("Work", systemImage: "checklist")
        } description: {
            Text(message)
        } actions: {
            if let retry { Button("Try Again", action: retry) }
        }
        .accessibilityIdentifier("project-work-unavailable")
    }
}
