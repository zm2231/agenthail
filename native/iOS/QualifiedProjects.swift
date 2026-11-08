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
}
