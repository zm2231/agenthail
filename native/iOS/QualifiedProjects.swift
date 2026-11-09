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

    var tileLeads: [String: ProjectTiles.Lead] {
        projects.reduce(into: [:]) { leads, entry in
            guard let lead = entry.value.state.leads.first, let id = ProjectContents.leadSessionID(lead, of: entry.value), let session = session(id) else { return }
            leads[entry.key] = ProjectTiles.Lead(session)
        }
    }

    func colourID(of projectID: String) -> String {
        projects[projectID].map { ProjectTiles.colourID($0.state.id, owner: $0.hostID) } ?? projectID
    }
}
