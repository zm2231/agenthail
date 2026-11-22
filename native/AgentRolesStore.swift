import Foundation
import Observation

struct AgentRolesRead: Sendable {
    let view: AgentRolesView
    let readOnlyReason: String?
}

protocol AgentRolesTransport: Sendable {
    func read() async throws -> AgentRolesRead
    func write(_ request: AgentRoleRequest) async throws -> AgentRolesView
}

struct AgentRolesAPITransport: AgentRolesTransport {
    let api: AgenthailAPI

    func read() async throws -> AgentRolesRead {
        AgentRolesRead(view: try await api.agentRoles(), readOnlyReason: nil)
    }

    func write(_ request: AgentRoleRequest) async throws -> AgentRolesView {
        try await api.changeAgentRole(request)
    }
}

@MainActor
@Observable
final class AgentRolesStore {
    private(set) var view: AgentRolesView?
    private(set) var loadError: String?
    private(set) var saveError: String?
    private(set) var readOnlyReason: String?
    private(set) var busy = false
    private(set) var hostName: String?
    @ObservationIgnored private var transport: (any AgentRolesTransport)?
    @ObservationIgnored private var hostID: String?
    @ObservationIgnored private var loads = 0

    var editable: Bool { view != nil && readOnlyReason == nil && !busy }

    func attach(_ transport: (any AgentRolesTransport)?, hostID: String? = nil, hostName: String? = nil) {
        let moved = hostID != self.hostID
        self.transport = transport
        self.hostID = hostID
        self.hostName = hostName
        if moved {
            view = nil
            saveError = nil
            loadError = nil
        }
    }

    func load() async {
        guard let transport else { return }
        loads += 1
        let attempt = loads
        do {
            let read = try await transport.read()
            guard attempt == loads else { return }
            view = read.view
            readOnlyReason = read.readOnlyReason
            loadError = nil
        } catch is CancellationError {
        } catch {
            guard attempt == loads else { return }
            loadError = AgenthailAPIError.userMessage(error)
        }
    }

    @discardableResult
    func send(_ request: AgentRoleRequest) async -> Bool {
        guard let transport, !busy else { return false }
        busy = true
        defer { busy = false }
        saveError = nil
        do {
            let updated = try await transport.write(request)
            loads += 1
            view = updated
            return true
        } catch LocalSocketUnavailable.readOnly(let reason) {
            readOnlyReason = reason
            saveError = reason
            return false
        } catch {
            saveError = AgenthailAPIError.userMessage(error)
            return false
        }
    }

    func clearSaveError() { saveError = nil }

    func receive(type: String, source: String?, hostID: String? = nil) {
        guard AgentRolesRefresh.refetches(type: type, source: source), view != nil || loadError != nil, hostID == nil || hostID == self.hostID else { return }
        Task { await load() }
    }
}

enum LocalSocketUnavailable: Error {
    case readOnly(String)
}
