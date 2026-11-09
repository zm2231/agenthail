import Foundation

extension ProjectContents {
    static func key(of project: ListedProject) -> String { HostCatalog.qualify(project.hostID, project.state.id) }

    static func leadSessionID(_ lead: ProjectState.Lead, of project: ListedProject) -> String? {
        guard let sessionID = lead.sessionId, !sessionID.isEmpty else { return nil }
        let owner = lead.host.flatMap { $0.state == "local" ? nil : $0.id } ?? project.hostID
        return HostCatalog.qualify(owner, sessionID)
    }

    static func leadsLine(_ project: ListedProject) -> String? {
        let leads = project.state.leads
        guard leads.count > 1 else { return nil }
        let labels = HostNames.labels(project.state.checkouts.map(\.host) + leads.compactMap(\.host))
        return leads.map { lead in
            let host = lead.host.flatMap { labels[$0.id] } ?? ""
            return host.isEmpty ? "@\(lead.name)" : "@\(lead.name) on \(host)"
        }.joined(separator: " · ")
    }

    static func build(_ project: ListedProject, sessions: [SessionState]) -> ProjectContents {
        let lead = project.state.leads.first
        return build(lead: lead, leadID: lead.flatMap { leadSessionID($0, of: project) }, projectID: key(of: project), sessions: sessions, computers: Set(project.state.checkouts.map(\.host.id)).count)
    }
}
