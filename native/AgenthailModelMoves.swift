import Foundation

extension AgenthailModel {
    var moveTargets: [CreationHost] { SessionMoveAvailability.targets(daemon?.hosts ?? []) }

    func canMove(_ session: SessionState) -> Bool {
        !movesSent.contains(session.id) && SessionMoveAvailability.offered(session, local: session.remoteHostID == nil, hosts: daemon?.hosts ?? [])
    }

    func move(_ session: SessionState, to host: CreationHost) {
        send(SessionMoveRequest(sessionId: session.id, host: host.id), title: session.displayName, hostName: host.name)
    }

    func retryMove(_ notice: SessionMoveNotice) {
        guard let retry = notice.retry else { return }
        let name = daemon?.hosts?.first { $0.id == retry.host }?.name ?? retry.host
        send(retry, title: session(notice.subject)?.displayName ?? notice.subject, hostName: name)
    }

    private func send(_ request: SessionMoveRequest, title: String, hostName: String) {
        guard let api, !movesSent.contains(request.sessionId) else { return }
        movesSent.insert(request.sessionId)
        let here = daemon?.host?.name ?? "this computer"
        Task {
            let outcome = await api.moveSession(request, hostName: hostName)
            movesSent.remove(request.sessionId)
            var notice = SessionMoveNotice.of(outcome, request: request, session: title, hostName: hostName, here: here)
            notice.subject = request.sessionId
            moveNotices.add(notice)
            _ = await refresh(fresh: true)
        }
    }
}
