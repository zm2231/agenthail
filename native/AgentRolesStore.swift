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

struct AgentRolesTimeout: LocalizedError, Equatable {
    let seconds: Int

    var errorDescription: String? { "Agenthail did not answer within \(seconds) seconds. Try again in a moment." }
}

struct AgentRolesAPITransport: AgentRolesTransport {
    static let timeout = 5
    let api: AgenthailAPI

    func read() async throws -> AgentRolesRead {
        AgentRolesRead(view: try await bounded { try await api.agentRoles(timeout: TimeInterval(Self.timeout)) }, readOnlyReason: nil)
    }

    func write(_ request: AgentRoleRequest) async throws -> AgentRolesView {
        try await bounded { try await api.changeAgentRole(request, timeout: TimeInterval(Self.timeout)) }
    }

    private func bounded<T>(_ work: () async throws -> T) async throws -> T {
        do {
            return try await work()
        } catch let error as URLError where error.code == .timedOut {
            throw AgentRolesTimeout(seconds: Self.timeout)
        }
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
    @ObservationIgnored private var generation = 0

    var editable: Bool { view != nil && readOnlyReason == nil && !busy }

    func attach(_ transport: (any AgentRolesTransport)?, hostID: String? = nil, hostName: String? = nil) {
        let moved = hostID != self.hostID
        if moved { generation += 1 }
        self.transport = transport
        self.hostID = hostID
        self.hostName = hostName
        if moved {
            view = nil
            saveError = nil
            loadError = nil
            readOnlyReason = nil
            busy = false
        }
    }

    func load() async {
        guard let transport else { return }
        loads += 1
        let attempt = loads, attached = generation
        do {
            let read = try await transport.read()
            guard attempt == loads, attached == generation else { return }
            view = read.view
            readOnlyReason = read.readOnlyReason
            loadError = nil
        } catch is CancellationError {
        } catch {
            guard attempt == loads, attached == generation else { return }
            loadError = AgenthailAPIError.userMessage(error)
        }
    }

    @discardableResult
    func send(_ request: AgentRoleRequest) async -> Bool {
        guard let transport, !busy else { return false }
        let attached = generation
        busy = true
        defer { if attached == generation { busy = false } }
        saveError = nil
        do {
            let updated = try await transport.write(request)
            guard attached == generation else { return false }
            loads += 1
            view = updated
            return true
        } catch LocalSocketUnavailable.readOnly(let reason) {
            guard attached == generation else { return false }
            readOnlyReason = reason
            saveError = reason
            return false
        } catch {
            guard attached == generation else { return false }
            saveError = AgenthailAPIError.userMessage(error)
            return false
        }
    }

    func clearSaveError() { saveError = nil }

    func receive(type: String, source: String?, hostID: String? = nil) {
        guard AgentRolesRefresh.refetches(type: type, source: source), view != nil, hostID == nil || hostID == self.hostID else { return }
        Task { await load() }
    }
}

enum LocalSocketUnavailable: Error {
    case readOnly(String)
}
