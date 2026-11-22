import Foundation

struct MacAgentRolesTransport: AgentRolesTransport {
    static let readOnlyReason = "Update the Agenthail daemon to change roles. Roles read here as they are."

    let socket: LocalSocketHTTP
    let api: AgenthailAPI?

    func read() async throws -> AgentRolesRead {
        do {
            return AgentRolesRead(view: try await socket.json("GET", "/api/v1/agents"), readOnlyReason: nil)
        } catch LocalSocketError.noSocket {
            guard let api else { throw LocalSocketError.noSocket }
            return AgentRolesRead(view: try await AgentRolesAPITransport(api: api).read().view, readOnlyReason: Self.readOnlyReason)
        }
    }

    func write(_ request: AgentRoleRequest) async throws -> AgentRolesView {
        do {
            return try await socket.json("POST", "/api/v1/agents", body: JSONEncoder().encode(request))
        } catch LocalSocketError.noSocket {
            throw LocalSocketUnavailable.readOnly(Self.readOnlyReason)
        }
    }
}
