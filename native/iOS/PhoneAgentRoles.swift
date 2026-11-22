import Foundation

extension AgenthailIOSModel {
    var agentRolesHost: PairedHostStatus? { hosts.first { $0.reachable } }

    func attachAgentRoles() {
        guard let host = agentRolesHost, let connection = connection(host.id) else {
            agentRoles.attach(nil)
            return
        }
        agentRoles.attach(AgentRolesAPITransport(api: connection.api), hostID: host.id, hostName: host.name)
    }

    func hostAgentRolesChanged(_ host: HostConnection) {
        guard connection(host.id) === host else { return }
        agentRoles.receive(type: "settings.updated", source: AgentRolesRefresh.source, hostID: host.id)
    }
}
