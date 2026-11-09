import Foundation

extension AgenthailIOSModel {
    func broadcast(to project: ListedProject, message: String, idempotencyKey: String) async throws -> String {
        guard let host = connection(project.hostID), host.shownReachable else {
            throw AgenthailAPIError.unavailable("Connect to the computer that holds \(project.state.displayName) first.")
        }
        let result = try await host.api.channelSend("#" + project.state.id, message: message, idempotencyKey: idempotencyKey)
        let sent = ProjectBroadcast.sent(result) { id in
            session(HostCatalog.qualify(project.hostID, id)).map(cells.title(of:)) ?? id
        }
        _ = await host.refresh(fresh: true)
        return sent
    }
}
