import Foundation

extension AgenthailIOSModel {
    func broadcast(to project: ListedProject, message: String, idempotencyKey: String) async throws -> String {
        var servers: [String] = []
        for server in [project.hostID] + project.servers where !servers.contains(server) { servers.append(server) }
        var covered: [String] = []
        var unreached: [String] = []
        var recipients: [ChannelSendResult.Recipient] = []
        var hosts: [ChannelSendResult.Host] = []
        var warnings: [String] = []
        var firstError: Error?
        for server in servers where !covered.contains(server) {
            guard let host = connection(server), host.shownReachable else {
                unreached.append(server)
                continue
            }
            let except = covered
            let key = server == project.hostID ? idempotencyKey : idempotencyKey + ":" + server
            do {
                let result = try await host.api.channelSend("#" + project.state.id, message: message, idempotencyKey: key, exceptHosts: except)
                covered.append(server)
                let legs = result.hosts ?? []
                let landed = legs.filter { ["reached", "held", "unknown"].contains($0.state) }
                if !except.isEmpty, result.exceptHosts == nil {
                    let twice = landed.filter { $0.id.map(except.contains) == true }.map { groupHostName($0.id ?? "", fallback: $0.name) }
                    if !twice.isEmpty {
                        warnings.append("\(host.name) runs an older Agenthail; \(twice.joined(separator: ", ")) may have received this twice.")
                    }
                }
                for leg in landed { if let id = leg.id, !covered.contains(id) { covered.append(id) } }
                recipients += result.recipients.map { recipient in
                    var recipient = recipient
                    recipient.sessionId = recipient.sessionId.map { HostCatalog.qualify(server, $0) }
                    return recipient
                }
                hosts += legs.filter { leg in !hosts.contains { $0.id != nil && $0.id == leg.id } }.map { leg in
                    ChannelSendResult.Host(id: leg.id, name: groupHostName(leg.id ?? "", fallback: leg.name), state: leg.state, detail: leg.detail)
                }
            } catch {
                firstError = firstError ?? error
                hosts.append(ChannelSendResult.Host(id: server, name: host.name, state: "refused", detail: AgenthailAPIError.userMessage(error)))
            }
        }
        guard covered.contains(where: servers.contains) else {
            if let firstError { throw firstError }
            throw AgenthailAPIError.unavailable("Connect to the computer that holds \(project.state.displayName) first.")
        }
        for server in unreached where !covered.contains(server) {
            hosts.append(ChannelSendResult.Host(id: server, name: groupHostName(server, fallback: server), state: "unreached"))
        }
        let held = hosts.filter { ["held", "unknown"].contains($0.state) }.map(\.name)
        let joined = ChannelSendResult(recipients: recipients, hosts: hosts, unreachable: held)
        let sent = ProjectBroadcast.sent(joined) { id in
            session(id).map(cells.title(of:)) ?? HostCatalog.owner(of: id).map { String(id.dropFirst($0.count + HostCatalog.separator.count)) } ?? id
        }
        for server in covered where servers.contains(server) { _ = await connection(server)?.refresh(fresh: true) }
        return ([sent] + warnings).joined(separator: " ")
    }

    private func groupHostName(_ id: String, fallback: String) -> String {
        connection(id)?.name ?? snapshot?.daemon.hosts?.first { $0.id == id }?.name ?? fallback
    }
}
