import Foundation

extension AgenthailIOSModel {
    var qualifiedProjects: [ProjectState] {
        projectOrder.compactMap { id in
            projects[id].map { listed in
                let state = listed.state
                return ProjectState(id: id, displayName: state.displayName, checkouts: state.checkouts, coordinators: state.coordinators,
                                    archived: state.archived, activity: state.activity, linked: state.linked)
            }
        }
    }

    func colourID(of projectID: String) -> String {
        projects[projectID].map { ProjectTiles.colourID($0.state.id, owner: $0.hostID) } ?? projectID
    }
}
