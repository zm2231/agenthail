import Foundation

extension AgenthailIOSModel {
    func addProject(path: String, title: String?, host: String?) async -> ProjectChange {
        await projectChange(refreshing: host) {
            let (api, target) = try self.reach(host)
            let (project, created) = try await api.addProject(path: path, title: title, host: target.remote)
            let name = project?.displayName ?? "This folder"
            return created ? "Added project \(name)." : "\(name) is already a project."
        }
    }

    func addFolder(of session: SessionState) async -> ProjectChange {
        await projectChange(refreshing: nil) {
            let (connection, _) = try self.resolveServer(session.id)
            let owner = HostCatalog.owner(of: session.id)
            let (project, created) = try await connection.api.addProject(path: session.cwd ?? "", host: owner == connection.id ? nil : owner)
            let name = project?.displayName ?? "This folder"
            return created ? "Added project \(name)." : "\(name) is already a project."
        }
    }

    func makeLead(_ sessionID: String, replace: Bool) async -> ProjectChange {
        await projectChange(refreshing: nil) {
            let (api, route) = try self.resolve(sessionID)
            let lead = try await api.makeLead(sessionID: route.sessionID, replace: replace)
            return "@\(lead.name) leads \(lead.projectRoot)."
        }
    }

    func renameProject(_ project: ListedProject, to title: String) async -> ProjectChange {
        let owners = project.state.checkouts.filter { $0.added == true }.map { ProjectRouting.owner($0.host, of: project) }
        return await projectChange(refreshing: nil) {
            try await self.eachOwner(of: owners, in: project, done: "Renamed to \(title).") { api, projectID in
                try await api.renameProject(projectID, title: title)
            }
        }
    }

    func removeProject(_ projectID: String, of project: ListedProject) async -> ProjectChange {
        let owner = HostCatalog.owner(of: projectID) ?? project.hostID
        return await projectChange(refreshing: nil) {
            let (api, target) = try self.reach(owner)
            try await api.projectAction("project-remove", projectID: target.remote == nil ? ProjectRouting.local(projectID, of: owner) : projectID)
            return "Project removed."
        }
    }

    func projectToolsRoute(_ project: ListedProject) -> (host: String, projectID: String)? {
        ProjectToolsRoute.target(project) { host in self.connections[host] != nil && self.catalog.target(forHost: host)?.takesSettings == true }
    }

    func setProjectTools(_ project: ListedProject, on: Bool) async -> ProjectChange {
        await projectChange(refreshing: nil) {
            guard let route = self.projectToolsRoute(project) else { throw AgenthailAPIError.unavailable("The computer that has \(project.state.displayName) can't be reached.") }
            let (api, _) = try self.reach(route.host)
            try await api.projectSetting(projectID: route.projectID, setting: "mcp", value: on ? "on" : "off")
            return ProjectMenu.toolsChanged(project.state.displayName, on: on)
        }
    }

    func clearLead(of project: ListedProject) async -> ProjectChange {
        let owners = project.state.leads.map { ProjectRouting.owner($0.host, of: project) }
        return await projectChange(refreshing: nil) {
            try await self.eachOwner(of: owners, in: project, done: "\(project.state.displayName) has no lead now.") { api, projectID in
                try await api.clearLead(projectID: projectID)
            }
        }
    }

    func brief(_ project: ListedProject, lead: ProjectState.Lead, idempotencyKey: String) async throws -> String {
        let (api, target) = try reach(ProjectRouting.owner(lead.host, of: project))
        let projectID = target.remote.map { $0 + HostCatalog.separator + project.state.id } ?? project.state.id
        let reply = try await api.projectBrief(projectID: projectID, idempotencyKey: idempotencyKey)
        _ = await connection(target.hostID)?.refresh(fresh: true)
        return ProjectBrief.notice(reply, lead: lead.name)
    }

    func startLead(of project: ListedProject, in checkout: ProjectState.Checkout, surface: String, name: String, replace: Bool) async -> ProjectChange {
        await projectChange(refreshing: nil) {
            let (api, target) = try self.reach(ProjectRouting.owner(checkout.host, of: project))
            let lead = try await api.startLead(surface: surface, folder: checkout.rootPath, host: target.remote, name: name, replace: replace)
            return "@\(lead.name) leads \(project.state.displayName); its first message starts it."
        }
    }

    private func eachOwner(of owners: [String], in project: ListedProject, done: String, _ action: (AgenthailAPI, String) async throws -> Void) async throws -> String {
        var routes: [(owner: String, target: HostTarget)] = []
        var unreached: [String] = []
        for owner in owners.isEmpty ? [project.hostID] : owners where !routes.contains(where: { $0.owner == owner }) && !unreached.contains(owner) {
            guard let target = catalog.target(forHost: owner) else { unreached.append(owner); continue }
            routes.append((owner, target))
        }
        func send(_ route: (owner: String, target: HostTarget)) async throws {
            guard let api = connections[route.target.hostID]?.api else { throw AgenthailAPIError.unavailable("\(hostName(route.owner)) can't be reached.") }
            try await action(api, route.target.remote.map { $0 + HostCatalog.separator + project.state.id } ?? project.state.id)
        }
        if unreached.isEmpty, routes.count == 1 {
            try await send(routes[0])
            return done
        }
        var failures = unreached.map { "\(hostName($0)) can't be reached" }
        var succeeded = false
        for route in routes {
            do {
                try await send(route)
                succeeded = true
            } catch {
                failures.append("\(hostName(route.owner)): \(AgenthailAPIError.userMessage(error))")
            }
        }
        guard !failures.isEmpty else { return done }
        throw AgenthailAPIError.unavailable((succeeded ? done + " Not changed on " : "Not changed on ") + failures.joined(separator: "; ") + ".")
    }

    private func hostName(_ hostID: String) -> String {
        connections[hostID]?.name ?? catalog.hosts.first { $0.id == hostID }?.name ?? hostID
    }

    func listed(_ state: ProjectState) -> ListedProject? {
        projects.values.first { $0.state == state }
    }

    func canAddFolder(_ session: SessionState) -> Bool {
        ProjectMenu.canAddFolder(session) && routing.route(for: session.id) != nil
    }

    func canMakeLead(_ session: SessionState) -> Bool {
        guard ProjectMenu.canMakeLead(session, projects: []), routing.route(for: session.id) != nil else { return false }
        return !projects.values.contains { listed in
            listed.state.leads.contains { lead in
                guard let sessionID = lead.sessionId else { return false }
                return HostCatalog.qualify(ProjectRouting.owner(lead.host, of: listed), sessionID) == session.id
            }
        }
    }

    func settle(_ change: ProjectChange) {
        operationError = change.error
    }

    func creationHost(for checkout: ProjectState.Checkout, of project: ListedProject) -> String {
        ProjectRouting.owner(checkout.host, of: project)
    }

    private func projectChange(refreshing hostID: String?, _ body: () async throws -> String) async -> ProjectChange {
        let change = await ProjectChange.run(body)
        if let hostID, connections[hostID] != nil { await refresh(host: hostID) } else { _ = await refresh(fresh: true) }
        change.announce(in: projectNotice)
        return change
    }

    func setProjectArchived(_ projectID: String, _ archived: Bool) async {
        let action = archived ? "project-archive" : "project-unarchive"
        do {
            guard let owner = HostCatalog.owner(of: projectID) else {
                try await setRepositoryArchived(projectID, action: action)
                operationError = nil
                return
            }
            let (api, target) = try reach(owner)
            let id = target.remote == nil ? String(projectID.dropFirst(owner.count + HostCatalog.separator.count)) : projectID
            try await api.projectAction(action, projectID: id)
            operationError = nil
            await refresh(host: target.hostID)
        } catch {
            operationError = error.localizedDescription
        }
    }

    // A repository project has one id on every computer; each computer that
    // serves one of its sessions archives its own checkout.
    private func setRepositoryArchived(_ projectID: String, action: String) async throws {
        let servers = Set((snapshot?.sessions ?? []).filter { $0.project?.id == projectID }.compactMap { catalog.server(of: $0.id) })
        let reachable = hostOrder.filter { servers.contains($0) && connections[$0]?.shownReachable == true }
        guard !reachable.isEmpty else { throw AgenthailAPIError.unavailable("Connect to your computer first.") }
        var failures: [String] = []
        for hostID in reachable {
            guard let connection = connections[hostID] else { continue }
            do {
                try await connection.api.projectAction(action, projectID: projectID)
            } catch {
                failures.append("\(connection.name): \(AgenthailAPIError.userMessage(error))")
            }
            await refresh(host: hostID)
        }
        if !failures.isEmpty { throw AgenthailAPIError.unavailable(failures.joined(separator: "\n")) }
    }
}
