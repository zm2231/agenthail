import Foundation

extension AgenthailIOSModel {
    func moveTargets(_ session: SessionState) -> [CreationHost] {
        SessionMoveAvailability.targets(runningServer(session.id)?.connection.snapshot?.daemon.hosts ?? [])
    }

    func canMove(_ session: SessionState) -> Bool {
        guard !movesSent.contains(session.id), let server = runningServer(session.id) else { return false }
        return SessionMoveAvailability.offered(session, local: true, hosts: server.connection.snapshot?.daemon.hosts ?? [])
    }

    func move(_ session: SessionState, to host: CreationHost) {
        guard let server = runningServer(session.id) else { return }
        send(SessionMoveRequest(sessionId: server.route.sessionID, host: host.id), sessionID: session.id, title: session.displayName, hostName: host.name)
    }

    func retryMove(_ notice: SessionMoveNotice) {
        guard let retry = notice.retry else { return }
        let name = runningServer(notice.subject)?.connection.snapshot?.daemon.hosts?.first { $0.id == retry.host }?.name ?? retry.host
        send(retry, sessionID: notice.subject, title: session(notice.subject)?.displayName ?? notice.subject, hostName: name)
    }

    private func send(_ request: SessionMoveRequest, sessionID: String, title: String, hostName: String) {
        guard !movesSent.contains(sessionID), let server = runningServer(sessionID) else { return }
        movesSent.insert(sessionID)
        let here = server.connection.snapshot?.daemon.host?.name ?? "that computer"
        Task {
            let outcome = await server.connection.api.moveSession(request, hostName: hostName)
            movesSent.remove(sessionID)
            var notice = SessionMoveNotice.of(outcome, request: request, session: title, hostName: hostName, here: here)
            notice.subject = sessionID
            moveNotice = notice
            _ = await server.connection.refresh(fresh: true)
        }
    }
}
