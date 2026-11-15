import SwiftUI

struct ProjectWorkPane: View {
    var model: AgenthailModel
    let project: ProjectState

    var body: some View {
        let place = ProjectWork.place(project, preferring: model.daemon?.host?.id ?? "")
        let catalog = model.extensionCatalogs.first { $0.hostID == place.hostID }
        Group {
            if let catalog, let api = catalog.api {
                switch catalog.list == nil && catalog.error != nil ? .unavailable(catalog.error ?? "") : ProjectWork.page(catalog.list, projectID: place.projectID, origin: api.origin, hostName: catalog.hostName) {
                case .reading:
                    ProgressView().controlSize(.small).frame(maxWidth: .infinity, minHeight: 60)
                case .shown(let url):
                    let controller = ExtensionPages.controller(id: ProjectWork.controllerID(place), hostID: place.hostID)
                    if case .failed(let message) = controller.phase {
                        unavailable(message) { controller.reload() }
                    } else {
                        ExtensionPageHost(controller: controller, url: url, signIn: catalog.signIn)
                            .frame(height: controller.contentHeight ?? ProjectWork.placeholderHeight)
                            .onAppear { controller.followContentHeight() }
                            .accessibilityIdentifier("project-work-page")
                    }
                case .unavailable(let message):
                    unavailable(message) { catalog.refresh() }
                }
            } else {
                unavailable("This Mac can't reach the computer that holds this project's work list.", retry: nil)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .task(id: place) {
            if let catalog, !catalog.loading { catalog.refresh() }
        }
    }

    private func unavailable(_ message: String, retry: (() -> Void)?) -> some View {
        HStack(alignment: .firstTextBaseline) {
            Text(message).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 8)
            if let retry { Button("Try Again", action: retry) }
        }
        .accessibilityIdentifier("project-work-unavailable")
    }
}
