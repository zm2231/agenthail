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
        InboxStops.build(replacements: leadReplacements, inbox: decisionFeed.inbox)
    }

    var needsYouCount: Int { inboxStops.count }

    func needsYou(_ stops: InboxStops) -> [String: Int] {
        stops.needsYou(in: projects) { [self] in session($0)?.project?.id }
    }
}
