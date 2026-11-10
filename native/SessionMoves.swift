import Foundation

struct SessionMove: Codable, Hashable {
    let state: String
    let host: String
    var hostName: String? = nil
    var root: String? = nil
    var detail: String? = nil

    var target: String { root.map { "account \($0)" } ?? hostName ?? host }

    var label: String {
        switch state {
        case "holding": return "Moving to \(target): holding"
        case "copying": return "Moving to \(target): copying"
        case "settling": return "Move to \(target) unsettled"
        default: return "Moving to \(target)"
        }
    }

    static let composerNote = "Messages wait until the move finishes"
}

enum SessionMoveAvailability {
    static func offered(_ session: SessionState, local: Bool, hosts: [HostHealth]) -> Bool {
        local && ["claude", "codex"].contains(session.surface) && session.subagent == nil && !session.isReadOnly && session.move == nil && !hosts.isEmpty
    }

    static func targets(_ hosts: [HostHealth]) -> [CreationHost] {
        hosts.map { CreationHost(id: $0.id, name: $0.name, state: $0.state) }
    }
}

struct SessionMoveRequest: Encodable, Equatable {
    let sessionId: String
    let host: String
    var stopTerminal = false

    static let timeout: TimeInterval = 31 * 60
}

struct SessionMoveSummary: Equatable {
    let hostName: String
    var stopped: String? = nil
    var mcpServers: [String] = []
    var droppedRelays: [String] = []
}

enum SessionMoveOutcome: Equatable {
    case moved(SessionMoveSummary)
    case unsettled
    case busy(String)
    case needsConsent
    case refused(String)
    case lost

    private struct Answer: Decodable {
        struct Failure: Decodable {
            let code: String?
            let message: String?
        }
        struct Host: Decodable { let name: String }
        struct DroppedRelay: Decodable {
            let relayId: Int64
            let toSession: String
        }
        struct Result: Decodable {
            let host: Host?
            let stopped: String?
            let mcpServersWithoutCredentials: [String]?
            let droppedRelays: [DroppedRelay]?
        }
        let error: Failure?
        let busy: String?
        let result: Result?
    }

    init?(status: Int, body: Data, hostName: String) {
        guard let answer = try? JSONDecoder().decode(Answer.self, from: body) else { return nil }
        let message = answer.error?.message ?? ""
        switch (status, answer.error?.code) {
        case (200, nil):
            guard let result = answer.result else { return nil }
            self = .moved(SessionMoveSummary(
                hostName: result.host?.name ?? hostName,
                stopped: result.stopped,
                mcpServers: result.mcpServersWithoutCredentials ?? [],
                droppedRelays: (result.droppedRelays ?? []).map { "Relay #\($0.relayId) was not taken over: session \($0.toSession), which it sends to, left this computer's view during the move." }))
        case (202, "move_outcome_unknown"): self = .unsettled
        case (409, "move_busy"): self = .busy(answer.busy ?? message)
        case (409, "move_needs_consent"): self = .needsConsent
        case (409, "move_refused"), (502, "move_failed"): self = .refused(message)
        default: return nil
        }
    }

    @MainActor
    static func from(_ error: Error) -> SessionMoveOutcome {
        if let error = error as? URLError, !ActionRetryKeys.notSent.contains(error.code) { return .lost }
        return .refused(AgenthailAPIError.userMessage(error))
    }
}

struct SessionMoveNotice: Identifiable, Equatable {
    let id = UUID()
    let title: String
    let message: String
    var retry: SessionMoveRequest? = nil
    var subject = ""

    static func == (lhs: Self, rhs: Self) -> Bool { lhs.id == rhs.id }

    static func of(_ outcome: SessionMoveOutcome, request: SessionMoveRequest, session: String, hostName: String, here: String) -> SessionMoveNotice {
        switch outcome {
        case .moved(let summary):
            var lines: [String] = []
            if let stopped = summary.stopped {
                lines.append(stopped == "host" ? "Stopped the Claude process Agenthail ran for it." : "Stopped the \(stopped) that held it.")
            }
            if !summary.mcpServers.isEmpty {
                lines.append((["These MCP servers moved without their environment and headers. Add their credentials again on \(summary.hostName):"] + summary.mcpServers).joined(separator: "\n"))
            }
            lines += summary.droppedRelays
            return SessionMoveNotice(title: "Moved to \(summary.hostName)", message: lines.joined(separator: "\n\n"))
        case .unsettled:
            return SessionMoveNotice(title: "The move to \(hostName) hasn't settled", message: "No computer writes to this conversation until \(hostName) answers. It settles when \(hostName) reconnects.")
        case .busy(let reason):
            return SessionMoveNotice(title: "Couldn't move \(session)", message: "\(session) is busy (\(reason)). Move it once it's idle.")
        case .needsConsent:
            var retry = request
            retry.stopTerminal = true
            return SessionMoveNotice(title: "Stop the terminal and move?", message: "A terminal is running \(session). Moving it stops that terminal.", retry: retry)
        case .refused(let message):
            return SessionMoveNotice(title: "Couldn't move \(session)", message: "\(message)\n\nIt stays on \(here).")
        case .lost:
            return SessionMoveNotice(title: "Lost the connection during the move", message: "The move to \(hostName) carries on without this app. The session's row shows how it ends.")
        }
    }
}

struct SessionMoveNotices {
    private(set) var pending: [SessionMoveNotice] = []

    var current: SessionMoveNotice? { pending.first }

    mutating func add(_ notice: SessionMoveNotice) { pending.append(notice) }

    mutating func dismiss(_ notice: SessionMoveNotice) { pending.removeAll { $0.id == notice.id } }
}

extension AgenthailAPI {
    @MainActor
    func moveSession(_ request: SessionMoveRequest, hostName: String) async -> SessionMoveOutcome {
        do {
            var urlRequest = authorizedRequest(path: "/api/v1/session-moves")
            urlRequest.httpMethod = "POST"
            urlRequest.timeoutInterval = SessionMoveRequest.timeout
            urlRequest.httpBody = try JSONEncoder().encode(request)
            urlRequest.setValue("application/json", forHTTPHeaderField: "Content-Type")
            let (data, response) = try await session.data(for: urlRequest)
            if let outcome = SessionMoveOutcome(status: (response as? HTTPURLResponse)?.statusCode ?? 0, body: data, hostName: hostName) {
                return outcome
            }
            try validate(response: response, data: data)
            return .refused(AgenthailAPIError.userMessage(AgenthailAPIError.invalidResponse))
        } catch {
            return SessionMoveOutcome.from(error)
        }
    }
}
