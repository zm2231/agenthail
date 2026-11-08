import Foundation

enum PaneScope: Equatable {
    case all
    case inbox
    case project(String)

    var projectID: String? {
        if case .project(let id) = self { return id }
        return nil
    }

    func keeps(projectID: String?) -> Bool {
        guard let scoped = self.projectID else { return true }
        return projectID == scoped
    }

    static func newSessionCheckout(_ project: ProjectState) -> ProjectState.Checkout? {
        let open = project.checkouts.filter { $0.archived != true }
        return open.first { $0.host.state == "local" } ?? open.first
    }

    static func holds(_ id: String, in projects: [ProjectState]) -> Bool {
        projects.contains { $0.id == id && $0.archived != true }
    }
}
