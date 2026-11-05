import Foundation

/// The daemon's page of a session's Claude subagents, in the daemon's order.
struct ClaudeSubagentList: Equatable {
    static let recentShown = 8

    let shown: [ClaudeSubagentLink]
    let earlier: Int
    let working: Int
    let total: Int

    init(_ links: [ClaudeSubagentLink], total reported: Int?, expanded: Bool) {
        working = links.filter { $0.working == true }.count
        total = max(reported ?? links.count, links.count)
        shown = expanded ? links : Array(links.prefix(working + Self.recentShown))
        earlier = total - shown.count
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
