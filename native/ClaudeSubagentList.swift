import Foundation

/// A host from before subagent paging sends its whole list and no total.
struct ClaudeSubagentList: Equatable {
    static let recentShown = 8

    let shown: [ClaudeSubagentLink]
    let earlier: Int
    let working: Int
    let total: Int

    init(_ links: [ClaudeSubagentLink], total reported: Int?, expanded: Bool) {
        let ordered = links.enumerated().sorted { lhs, rhs in
            let (a, b) = (lhs.element, rhs.element)
            if (a.working == true) != (b.working == true) { return a.working == true }
            let (aTime, bTime) = (Self.time(a.lastActive), Self.time(b.lastActive))
            if aTime != bTime { return aTime > bTime }
            return lhs.offset < rhs.offset
        }.map(\.element)
        working = ordered.filter { $0.working == true }.count
        total = max(reported ?? ordered.count, ordered.count)
        shown = expanded ? ordered : Array(ordered.prefix(working + Self.recentShown))
        earlier = total - shown.count
    }

    private static func time(_ value: String?) -> Date {
        value.flatMap(SessionTree.parseTimestamp) ?? .distantPast
    }

    var summary: String {
        let noun = total == 1 ? "subagent" : "subagents"
        return working > 0 ? "\(working) working, \(total) \(noun) in all" : "\(total) \(noun) in all"
    }

    var earlierLabel: String { "Show \(earlier) earlier" }
}

extension ClaudeSubagentLink {
    var title: String {
        if let description, !description.isEmpty { return description }
        if let agentType, !agentType.isEmpty { return agentType }
        return "Subagent \(agentId.prefix(8))"
    }

    var kindLabel: String? {
        guard let description, !description.isEmpty, let agentType, !agentType.isEmpty else { return nil }
        return agentType
    }
}
