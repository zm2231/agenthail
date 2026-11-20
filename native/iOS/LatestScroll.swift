enum LatestScroll: Equatable {
    case row(String)
    case edge

    // Setting the scroll position to the row it already holds is no change, so a repeat request goes to the edge.
    static func target(overflows: Bool, latest: String?, pinned: String?) -> LatestScroll {
        guard overflows, let latest, latest != pinned else { return .edge }
        return .row(latest)
    }
}

enum TurnReveal {
    static let olderPages = 3

    static func item(_ turn: String, in items: [TimelineItem]) -> String? {
        let rows = items.filter { $0.turnId == turn }
        return (rows.last { $0.kind == "message" && $0.role == "assistant" } ?? rows.last)?.id
    }

    static func row(_ turn: String, groups: [TimelineGroup]) -> String? {
        guard let id = item(turn, in: groups.flatMap(\.items)) else { return nil }
        return groups.first { $0.items.contains { $0.id == id } }?.id
    }
}
