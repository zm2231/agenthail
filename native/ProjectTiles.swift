import Foundation

struct ProjectTiles: Equatable {
    var shown: [ProjectState]
    var more: [ProjectState]

    static func order(_ projects: [ProjectState], limit: Int) -> ProjectTiles {
        let open = projects.filter { $0.archived != true && ($0.activity?.sessionCount ?? 0 > 0 || !$0.leads.isEmpty) }
        let recent = open.sorted { lhs, rhs in
            let left = lhs.activity?.lastActive.flatMap(TimestampParser.date) ?? .distantPast
            let right = rhs.activity?.lastActive.flatMap(TimestampParser.date) ?? .distantPast
            if left != right { return left > right }
            return lhs.displayName.localizedStandardCompare(rhs.displayName) == .orderedAscending
        }
        let shown = Array(recent.prefix(limit))
        let rest = projects.filter { project in !shown.contains { $0.id == project.id } }
            .sorted { $0.displayName.localizedStandardCompare($1.displayName) == .orderedAscending }
        return ProjectTiles(shown: shown, more: rest)
    }

    static func letter(_ name: String) -> String {
        name.first(where: { $0.isLetter || $0.isNumber }).map { String($0).uppercased() } ?? "·"
    }
}
