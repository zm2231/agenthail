enum LatestScroll: Equatable {
    case row(String)
    case edge

    // Setting the scroll position to the row it already holds is no change, so a repeat request goes to the edge.
    static func target(overflows: Bool, latest: String?, pinned: String?) -> LatestScroll {
        guard overflows, let latest, latest != pinned else { return .edge }
        return .row(latest)
    }
}
