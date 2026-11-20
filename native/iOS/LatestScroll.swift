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

    enum Found: Equatable {
        case settled(String)
        case partial
        case missing

        var settled: String? {
            if case .settled(let id) = self { return id }
            return nil
        }
    }

    static func isReply(_ item: TimelineItem) -> Bool {
        item.isSaid && (item.role ?? "assistant") == "assistant" && !item.isInstructions
    }

    // A turn whose reply isn't loaded may still have it on an older page, unless the turn's start is loaded too.
    static func find(_ turn: String, in items: [TimelineItem]) -> Found {
        let rows = items.filter { $0.turnId == turn }
        if let reply = rows.last(where: isReply) { return .settled(reply.id) }
        guard let last = rows.last else { return .missing }
        return items.first?.turnId == turn ? .partial : .settled(last.id)
    }

    static func row(of id: String, in groups: [TimelineGroup]) -> String? {
        groups.first { $0.items.contains { $0.id == id } }?.id
    }
}
