import Foundation

struct InboxPage {
    struct Host: Sendable {
        let id: String
        let name: String
        let api: AgenthailAPI
        var remote: String? = nil
    }

    private struct Answer {
        var active: [QueueState] = []
        var messages: InboxMessagePage?
    }

    static let hostTimeout: TimeInterval = 6
    static let historyPollInterval: Duration = .seconds(5)

    var active: [QueueState] = []
    var messages: [InboxMessage] = []
    var cursors: [String: Int64] = [:]
    var unreachable: [String] = []
    var outdated: [String] = []
    var answered: Set<String> = []

    var hasOlder: Bool { !cursors.isEmpty }
    var isEmpty: Bool { active.isEmpty && messages.isEmpty }

    var history: [InboxMessage] {
        let current = Set(active.map(\.listID))
        return messages.filter { message in
            guard !message.pending else { return false }
            guard let queueID = message.queueId else { return true }
            return !current.contains("\(message.hostID ?? "")/\(queueID)")
        }
    }

    // One unreachable computer must not hold the inbox, so hosts are asked at once with a short timeout.
    static func load(from hosts: [Host], before cursors: [String: Int64]? = nil, partial: (@MainActor (InboxPage) -> Void)? = nil) async throws -> InboxPage {
        let asked = cursors.map { cursors in hosts.filter { cursors[$0.id] != nil } } ?? hosts
        let (page, failure) = await withTaskGroup(of: (Host, Result<Answer, Error>).self) { group -> (InboxPage, Error?) in
            for host in asked {
                let before = cursors?[host.id]
                group.addTask {
                    do { return (host, .success(try await answer(from: host, before: before))) }
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
                    page.active = sortedActive(page.active + loaded.active.map { qualified($0, host: host.id) })
                    page.answered.insert(host.id)
                    if let messages = loaded.messages {
                        page.messages = sortedMessages(page.messages + messages.items.map { qualified($0, host: host.id) })
                        if let next = messages.nextBefore { page.cursors[host.id] = next }
                    } else {
                        page.outdated = (page.outdated + [host.name]).sorted()
                    }
                case .failure(let error):
                    page.unreachable = (page.unreachable + [host.name]).sorted()
                    if let before = cursors?[host.id] { page.cursors[host.id] = before }
                    failure = error
                }
                if answered < asked.count, let partial { await partial(page) }
            }
            return (page, page.isEmpty && page.unreachable.count == asked.count ? failure : nil)
        }
        if let failure { throw failure }
        return page
    }

    private static func answer(from host: Host, before: Int64?) async throws -> Answer {
        if let before {
            return Answer(messages: try await host.api.inbox(host: host.remote, before: before, timeout: hostTimeout))
        }
        async let queue = host.api.activeQueue(host: host.remote, timeout: hostTimeout)
        async let messages = messagesUnlessOutdated(from: host)
        let (rows, page) = try await (queue, messages)
        // A computer older than the inbox ignores `host` and lists its own queue, so those rows are not the remote's.
        let trusted = page == nil && host.remote != nil ? [] : rows
        return Answer(active: trusted.filter { !$0.isHistorical }, messages: page)
    }

    private static func messagesUnlessOutdated(from host: Host) async throws -> InboxMessagePage? {
        do { return try await host.api.inbox(host: host.remote, timeout: hostTimeout) }
        catch AgenthailAPIError.olderHost { return nil }
    }

    static func qualified(_ item: QueueState, host hostID: String) -> QueueState {
        var item = item
        item.hostID = hostID
        item.sessionId = HostCatalog.qualify(hostID, item.sessionId)
        item.sourceSessionId = item.sourceSessionId.map { HostCatalog.qualify(hostID, $0) }
        return item
    }

    static func qualified(_ item: InboxMessage, host hostID: String) -> InboxMessage {
        var item = item
        item.hostID = hostID
        item.sessionId = HostCatalog.qualify(hostID, item.sessionId)
        item.sourceSessionId = item.sourceSessionId.map { HostCatalog.qualify(hostID, $0) }
        return item
    }

    private static func sortedActive(_ items: [QueueState]) -> [QueueState] {
        items.sorted { lhs, rhs in
            switch (lhs.queuedDate, rhs.queuedDate) {
            case let (left?, right?) where left != right: return left < right
            default: return lhs.hostID == rhs.hostID ? lhs.id < rhs.id : (lhs.hostID ?? "") < (rhs.hostID ?? "")
            }
        }
    }

    private static func sortedMessages(_ items: [InboxMessage]) -> [InboxMessage] {
        items.sorted { lhs, rhs in
            switch (lhs.createdDate, rhs.createdDate) {
            case let (left?, right?) where left != right: return left > right
            default: return lhs.hostID == rhs.hostID ? lhs.id > rhs.id : (lhs.hostID ?? "") > (rhs.hostID ?? "")
            }
        }
    }

    // A host that has not answered keeps what was loaded from it, including how to load more.
    func refreshed(with first: InboxPage) -> InboxPage {
        let fresh = Set(first.messages.map(\.listID))
        let kept = messages.filter { message in
            let host = message.hostID ?? ""
            guard !fresh.contains(message.listID) else { return false }
            guard first.answered.contains(host) else { return true }
            guard let cursor = first.cursors[host] else { return false }
            return message.id < cursor
        }
        var merged = first
        merged.active = Self.sortedActive(first.active + active.filter { !first.answered.contains($0.hostID ?? "") })
        merged.messages = Self.sortedMessages(first.messages + kept)
        for host in Set(kept.compactMap(\.hostID)).union(cursors.keys.filter { !first.answered.contains($0) }) { merged.cursors[host] = cursors[host] }
        return merged
    }

    func appending(_ older: InboxPage) -> InboxPage {
        let known = Set(messages.map(\.listID))
        var merged = self
        merged.messages = Self.sortedMessages(messages + older.messages.filter { !known.contains($0.listID) })
        for host in cursors.keys where older.answered.contains(host) { merged.cursors[host] = older.cursors[host] }
        for host in older.unreachable where !merged.unreachable.contains(host) { merged.unreachable.append(host) }
        return merged
    }
}
