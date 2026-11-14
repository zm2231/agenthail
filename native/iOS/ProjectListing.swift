import Foundation

struct ListedProject: Equatable {
    let state: ProjectState
    let hostID: String
    var servers: [String] = []
}

enum ProjectListing {
    struct Server {
        let id: String
        let projects: [ProjectState]
    }

    // Mirrors catalogProjects (internal/daemon/projects.go): one project per id, one checkout per (host, local id), every computer's leads, archived only when every checkout is.
    static func merge(_ servers: [Server], name: (String) -> String?, reachable: (String) -> Bool) -> (projects: [String: ListedProject], order: [String]) {
        var entries: [String: Entry] = [:]
        var order: [String] = []
        for server in servers {
            for project in server.projects {
                let key = HostCatalog.qualify(server.id, project.id)
                if entries[key] == nil {
                    entries[key] = Entry(state: project, primary: server.id)
                    order.append(key)
                }
                entries[key]?.add(project, from: server.id)
            }
        }
        var listed: [String: ListedProject] = [:]
        for key in order {
            guard let entry = entries[key] else { continue }
            listed[key] = entry.listed(name: name, reachable: reachable)
        }
        return (listed, order)
    }

    private struct Placed<Item> {
        var item: Item
        let owner: String
        let direct: Bool
    }

    private struct Entry {
        var state: ProjectState
        let primary: String
        var servers: [String] = []
        var named = false
        var checkouts: [String: Placed<ProjectState.Checkout>] = [:]
        var checkoutOrder: [String] = []
        var leads: [String: Placed<ProjectState.Lead>] = [:]
        var leadOrder: [String] = []
        var linked: [ProjectState.Ref] = []
        var settings: [String: String] = [:]

        init(state: ProjectState, primary: String) {
            self.state = state
            self.primary = primary
        }

        mutating func add(_ project: ProjectState, from server: String) {
            if !servers.contains(server) { servers.append(server) }
            if let mcp = project.mcp { settings[server] = mcp }
            let addsName = project.checkouts.contains { $0.added == true && $0.host.state == "local" }
            if !named && addsName {
                named = true
                state = ProjectState(id: state.id, displayName: project.displayName, checkouts: state.checkouts, coordinators: state.coordinators, archived: state.archived, activity: state.activity, linked: state.linked)
            }
            for checkout in project.checkouts {
                let owner = checkout.host.state == "local" ? server : checkout.host.id
                let local = ProjectListing.local(checkout.projectId, of: owner)
                place(checkout, key: owner + HostCatalog.separator + local, owner: owner, direct: owner == server, into: &checkouts, order: &checkoutOrder)
            }
            for lead in project.leads {
                let owner = lead.host.map { $0.state == "local" ? server : $0.id } ?? server
                place(lead, key: owner + HostCatalog.separator + lead.name, owner: owner, direct: owner == server, into: &leads, order: &leadOrder)
            }
            for ref in project.linked ?? [] where !linked.contains(ref) { linked.append(ref) }
        }

        private func place<Item>(_ item: Item, key: String, owner: String, direct: Bool, into placed: inout [String: Placed<Item>], order: inout [String]) {
            if let existing = placed[key] {
                if direct && !existing.direct { placed[key] = Placed(item: item, owner: owner, direct: true) }
                return
            }
            placed[key] = Placed(item: item, owner: owner, direct: direct)
            order.append(key)
        }

        func listed(name: (String) -> String?, reachable: (String) -> Bool) -> ListedProject {
            func host(_ owner: String, was original: SessionHost?) -> SessionHost {
                if owner == primary { return SessionHost(id: primary, name: name(primary) ?? original?.name ?? primary, state: "local") }
                let state = reachable(owner) ? "connected" : original?.state == "connected" || original?.state == "local" ? "unreachable" : original?.state ?? "unreachable"
                return SessionHost(id: owner, name: name(owner) ?? original?.name ?? owner, state: state)
            }
            func rank(_ owner: String, _ hostName: String) -> (Int, String) { (owner == primary ? 0 : 1, hostName) }
            let merged = checkoutOrder.compactMap { checkouts[$0] }.map { placed in
                let local = ProjectListing.local(placed.item.projectId, of: placed.owner)
                let checkout = placed.item
                return ProjectState.Checkout(host: host(placed.owner, was: checkout.host),
                                             projectId: placed.owner == primary ? local : placed.owner + HostCatalog.separator + local,
                                             rootPath: checkout.rootPath, added: checkout.added, archived: checkout.archived, linkedFrom: checkout.linkedFrom)
            }.sorted { left, right in
                let (l, r) = (rank(left.host.id, left.host.name), rank(right.host.id, right.host.name))
                return l != r ? l < r : left.rootPath < right.rootPath
            }
            let mergedLeads = leadOrder.compactMap { leads[$0] }.map { placed in
                var lead = placed.item
                lead.host = host(placed.owner, was: lead.host)
                return lead
            }.sorted { left, right in
                rank(left.host?.id ?? primary, left.host?.name ?? "") < rank(right.host?.id ?? primary, right.host?.name ?? "")
            }
            let archived = merged.isEmpty ? state.archived : merged.allSatisfy { $0.archived == true }
            let shared = state.id.hasPrefix(HostCatalog.repositoryPrefix)
            let mcp = shared ? settings[primary] ?? servers.lazy.compactMap { settings[$0] }.first : settings[HostCatalog.owner(of: state.id) ?? primary]
            let project = ProjectState(id: state.id, displayName: state.displayName, checkouts: merged, coordinators: mergedLeads,
                                       archived: archived, activity: state.activity, linked: linked.isEmpty ? state.linked : linked, mcp: mcp)
            return ListedProject(state: project, hostID: primary, servers: servers)
        }
    }

    private static func local(_ projectID: String, of owner: String) -> String { ProjectRouting.local(projectID, of: owner) }
}

enum ProjectToolsRoute {
    // A folder's setting lives on the host that has it; a repository's on every host that holds it.
    static func target(_ project: ListedProject, takesSettings: (String) -> Bool) -> (host: String, projectID: String)? {
        let id = project.state.id
        if id.hasPrefix(HostCatalog.repositoryPrefix) {
            return ([project.hostID] + project.servers).first(where: takesSettings).map { ($0, id) }
        }
        let owner = HostCatalog.owner(of: id) ?? project.hostID
        return takesSettings(owner) ? (owner, ProjectRouting.local(id, of: owner)) : nil
    }
}

enum ProjectRouting {
    static func owner(_ host: SessionHost?, of project: ListedProject) -> String {
        guard let host, host.state != "local" else { return project.hostID }
        return host.id
    }

    static func local(_ projectID: String, of owner: String) -> String {
        let prefix = owner + HostCatalog.separator
        return projectID.hasPrefix(prefix) ? String(projectID.dropFirst(prefix.count)) : projectID
    }
}
