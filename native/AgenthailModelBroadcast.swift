import Foundation

extension AgenthailModel {
    func broadcast(to project: ProjectState, message: String, idempotencyKey: String) async throws -> String {
        guard let api else { throw AgenthailAPIError.unavailable("Agenthail isn't connected.") }
        let result = try await api.channelSend("#" + project.id, message: message, idempotencyKey: idempotencyKey)
        let sent = ProjectBroadcast.sent(result) { id in
            knownSessions.first { $0.id == id }.map(title(of:)) ?? id
        }
        _ = await refresh(fresh: true)
        return sent
    }
}
