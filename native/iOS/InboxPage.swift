import Foundation

struct InboxPage {
    struct Host: Sendable {
        let id: String
        let name: String
        let api: AgenthailAPI
    }

    static let hostTimeout: TimeInterval = 6

    var items: [QueueState] = []
    var cursors: [String: Int64] = [:]
    var unreachable: [String] = []
    var answered: Set<String> = []

    var hasOlder: Bool { !cursors.isEmpty }

    // One unreachable computer must not hold the inbox, so hosts are asked at once with a short timeout.
    static func load(from hosts: [Host], sessionID: String?, before cursors: [String: Int64]? = nil, partial: (@MainActor (InboxPage) -> Void)? = nil) async throws -> InboxPage {
        let asked = cursors.map { cursors in hosts.filter { cursors[$0.id] != nil } } ?? hosts
        let (page, failure) = await withTaskGroup(of: (Host, Result<QueuePage, Error>).self) { group -> (InboxPage, Error?) in
            for host in asked {
                let before = cursors?[host.id]
                group.addTask {
                    do { return (host, .success(try await host.api.queuePage(sessionID: sessionID, before: before, timeout: hostTimeout))) }
                    catch { return (host, .failure(error)) }
                }
            }
            var page = InboxPage()
            var failure: Error?
            var answered = 0
            for await (host, result) in group {
                answered += 1
                switch result {
                case .success(let loaded):
                    page.items += loaded.items.map { qualified($0, host: host.id) }.filter { sessionID == nil || $0.sessionId == sessionID }
                    page.items.sort(by: Self.newestFirst)
                    page.answered.insert(host.id)
                    if let next = loaded.nextBefore { page.cursors[host.id] = next }
                case .failure(let error):
                    page.unreachable.append(host.name)
                    page.unreachable.sort()
                    if let before = cursors?[host.id] { page.cursors[host.id] = before }
                    failure = error
                }
                if answered < asked.count, let partial { await partial(page) }
            }
            return (page, page.items.isEmpty && page.unreachable.count == asked.count ? failure : nil)
        }
        if let failure { throw failure }
        return page
    }

    static func qualified(_ item: QueueState, host hostID: String) -> QueueState {
        var item = item
        item.hostID = hostID
        item.sessionId = HostCatalog.qualify(hostID, item.sessionId)
        item.sourceSessionId = item.sourceSessionId.map { HostCatalog.qualify(hostID, $0) }
        return item
    }

    static func newestFirst(_ lhs: QueueState, _ rhs: QueueState) -> Bool {
        switch (lhs.queuedDate, rhs.queuedDate) {
        case let (left?, right?) where left != right: return left > right
        default: return lhs.hostID == rhs.hostID ? lhs.id > rhs.id : (lhs.hostID ?? "") > (rhs.hostID ?? "")
        }
    }

    // A host that has not answered keeps what was loaded from it, including how to load more.
    func refreshed(with first: InboxPage) -> InboxPage {
        let fresh = Set(first.items.map(\.listID))
        let kept = items.filter { item in
            let host = item.hostID ?? ""
            guard !fresh.contains(item.listID) else { return false }
            guard first.answered.contains(host) else { return true }
            guard item.isHistorical, let cursor = first.cursors[host] else { return false }
            return item.id < cursor
        }
        var merged = first
        merged.items = (first.items + kept).sorted(by: Self.newestFirst)
        for host in Set(kept.compactMap(\.hostID)).union(cursors.keys.filter { !first.answered.contains($0) }) { merged.cursors[host] = cursors[host] }
        return merged
    }

    func appending(_ older: InboxPage) -> InboxPage {
        let known = Set(items.map(\.listID))
        var merged = self
        merged.items = (items + older.items.filter { !known.contains($0.listID) }).sorted(by: Self.newestFirst)
        for host in cursors.keys where older.answered.contains(host) { merged.cursors[host] = older.cursors[host] }
        for host in older.unreachable where !merged.unreachable.contains(host) { merged.unreachable.append(host) }
        return merged
    }
}
