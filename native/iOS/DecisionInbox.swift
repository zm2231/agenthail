import Foundation

extension DecisionInbox {
    // One unreachable computer must not hold the decisions, so computers are asked at once with a short timeout.
    static func load(from hosts: [InboxPage.Host]) async -> DecisionInbox {
        await withTaskGroup(of: (InboxPage.Host, Result<DecisionsPage, Error>).self) { group in
            for host in hosts {
                group.addTask {
                    do { return (host, .success(try await host.api.decisions(host: host.remote, timeout: InboxPage.hostTimeout))) }
                    catch { return (host, .failure(error)) }
                }
            }
            var inbox = DecisionInbox()
            for await (host, result) in group {
                switch result {
                case .success(let page): inbox.add(page, host: host.id) { HostCatalog.qualify(host.id, $0) }
                case .failure(AgenthailAPIError.olderHost): inbox.outdated = (inbox.outdated + [host.name]).sorted()
                case .failure: inbox.unreachable = (inbox.unreachable + [host.name]).sorted()
                }
            }
            return inbox
        }
    }
}
