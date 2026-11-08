import Foundation

extension InboxStop {
    var paneID: String {
        if case .decision(let item) = self { return item.id }
        return id
    }

    var paneSummary: String {
        if case .decision(let item) = self { return DecisionInboxPane.summary(item) }
        return summary
    }
}

extension InboxStops {
    var entries: [InboxStop] {
        stopped + decisions.filter { $0.group == .holding }.map(InboxStop.decision) + decisions.filter { $0.group == .waiting }.map(InboxStop.decision)
    }

    func selected(_ id: String?) -> InboxStop? {
        entries.first { $0.paneID == id } ?? entries.first
    }

    func following(_ entry: InboxStop) -> String? {
        let list = entries
        guard let index = list.firstIndex(where: { $0.paneID == entry.paneID }) else { return nil }
        return list.indices.contains(index + 1) ? list[index + 1].paneID : nil
    }
}

extension AgenthailModel {
    var inboxStops: InboxStops {
        InboxStops.build(attention: attention, deliveries: deliveryProblems, replacements: leadReplacements, inbox: decisionFeed.inbox, localHosts: [""])
    }

    var needsYouCount: Int { inboxStops.count }

    func queuedText(_ stop: InboxStop) -> String? {
        guard case .queued(let item) = stop else { return nil }
        return queue.first { $0.id == item.queueId }?.message
    }

    func needsYou(_ stops: InboxStops) -> [String: Int] {
        stops.needsYou(in: projects) { [self] in session($0)?.project?.id }
    }

    func retryQueued(_ item: AttentionState) {
        settleQueued(item, action: "queue-retry")
    }

    func cancelQueued(_ item: AttentionState) {
        settleQueued(item, action: "queue-cancel")
    }

    private func settleQueued(_ item: AttentionState, action: String) {
        guard let api else { return }
        let position = snapshot?.attention.firstIndex { $0.id == item.id }
        snapshot?.attention.removeAll { $0.id == item.id }
        Task {
            do {
                try await api.action(action, queueID: item.queueId)
                operationError = nil
            } catch {
                operationError = AgenthailAPIError.userMessage(error)
                if let position, snapshot?.attention.contains(where: { $0.id == item.id }) == false {
                    snapshot?.attention.insert(item, at: min(position, snapshot?.attention.count ?? 0))
                }
            }
            _ = await refresh(fresh: true)
        }
    }
}
