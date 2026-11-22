import Foundation

extension AgenthailIOSModel {
    private func inboxHosts() throws -> [InboxPage.Host] {
        let direct = hostOrder.compactMap { connections[$0] }.filter(\.shownReachable).map { InboxPage.Host(id: $0.id, name: $0.name, api: $0.api) }
        let remote = catalog.hosts.compactMap { host -> InboxPage.Host? in
            guard let target = catalog.target(forHost: host.id), let peer = target.remote, let connection = connections[target.hostID] else { return nil }
            return InboxPage.Host(id: host.id, name: host.name, api: connection.api, remote: peer)
        }
        let hosts = direct + remote
        guard !hosts.isEmpty else { throw AgenthailAPIError.unavailable("Connect to your computer first.") }
        return hosts
    }

    func inboxPage(before cursors: [String: Int64]? = nil, partial: (@MainActor (InboxPage) -> Void)? = nil) async throws -> InboxPage {
        try await InboxPage.load(from: inboxHosts(), before: cursors, partial: partial)
    }

    func loadDecisions() async -> DecisionInbox {
        guard let hosts = try? inboxHosts() else { return DecisionInbox() }
        var inbox = await DecisionInbox.load(from: hosts)
        let older = hostOrder.compactMap { connections[$0] }.filter { $0.snapshot != nil && $0.snapshot?.decisionsRev == nil }.map(\.name)
        inbox.outdated = Array(Set(inbox.outdated + older)).sorted()
        return inbox
    }

    func dismissDecision(_ item: DecisionItem) async -> String? {
        guard decisionFeed.startDismissing(item.id) else { return nil }
        var failure: String?
        do {
            for dismissal in DecisionInbox.dismissals(of: item) {
                do {
                    if dismissal.workItem != nil, let reporter = try inboxHosts().first(where: { $0.id == dismissal.host }) {
                        try await reporter.api.dismissDecision(sessionID: DecisionInbox.localised(item.sessionId, own: reporter.id), workItem: dismissal.workItem, requestID: nil, host: reporter.remote ?? "")
                    } else {
                        let (api, route) = try resolve(item.sessionId)
                        try await api.dismissDecision(sessionID: route.sessionID, workItem: dismissal.workItem, requestID: dismissal.requestID)
                    }
                } catch where DecisionInbox.settled(error) {}
            }
        } catch {
            failure = AgenthailAPIError.userMessage(error)
        }
        decisionFeed.finishDismissing(item.id, failure: failure)
        decisionFeed.reload()
        return failure
    }

    func replyToParked(_ item: DecisionItem, words: String) async -> String? {
        do {
            let (api, route) = try resolve(item.sessionId)
            let wire = try api.instructionWire(sessionID: route.sessionID, message: DecisionInbox.reply(words, for: item), author: "person", attachments: [], idempotencyKey: UUID().uuidString)
            _ = try await api.send(wire)
            decisionFeed.replied(item.id, to: sessionName(item.sessionId))
            await refreshSession(item.sessionId)
            return nil
        } catch {
            return AgenthailAPIError.userMessage(error)
        }
    }
}
