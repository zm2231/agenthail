import SwiftUI

struct PhoneProjectWork: View {
    var model: AgenthailIOSModel
    let project: ListedProject

    var body: some View {
        let place = ProjectWork.place(project.state, preferring: project.hostID)
        let catalog = model.extensionCatalogs[place.hostID]
        Group {
            if let catalog, let api = catalog.api {
                switch catalog.list == nil && catalog.error != nil ? .unavailable(catalog.error ?? "") : ProjectWork.page(catalog.list, projectID: place.projectID, origin: api.origin, hostName: catalog.hostName) {
                case .reading:
                    ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
                case .shown(let url):
                    let controller = ExtensionPages.controller(id: ProjectWork.controllerID(place), hostID: place.hostID)
                    ExtensionPageView(controller: controller, url: url, signIn: catalog.signIn)
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
                unavailable("Connect to the computer that holds this project's work list to see it here.", retry: nil)
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
