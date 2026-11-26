import Foundation

struct ProjectTiles: Equatable {
    var shown: [ProjectState]
    var more: [ProjectState]

    struct Lead: Equatable {
        var lastActive: Date?
        var active: Bool

        static let recentWindow: TimeInterval = 7 * 24 * 60 * 60

        init(lastActive: Date?, active: Bool) {
            self.lastActive = lastActive
            self.active = active
        }

        init(currentReason: String?, waiting: Bool, lastActive: String?) {
            self.init(lastActive: lastActive.flatMap(ProjectTiles.millisecondDate), active: waiting || currentReason == "working" || currentReason == "queued")
        }

        init(_ session: SessionState) {
            self.init(currentReason: session.currentReason, waiting: session.waitingOn != nil, lastActive: session.lastActive)
        }

        func recent(at now: Date) -> Bool {
            active || lastActive.map { now.timeIntervalSince($0) <= Self.recentWindow } == true
        }
    }

    static func millisecondDate(_ value: String) -> Date? {
        let trimmed = value.replacingOccurrences(of: #"(\.\d{3})\d+"#, with: "$1", options: .regularExpression)
        return TimestampParser.date(trimmed).map { Date(timeIntervalSince1970: ($0.timeIntervalSince1970 * 1000).rounded() / 1000) }
    }

    static func order(_ projects: [ProjectState], limit: Int, waiting: [String: Int] = [:], leads: [String: Lead] = [:], now: Date = Date()) -> ProjectTiles {
        func active(_ project: ProjectState) -> Bool {
            project.activity?.sessionCount ?? 0 > 0 || !project.leads.isEmpty || waiting[project.id] ?? 0 > 0
        }
        func group(_ project: ProjectState) -> Int {
            if waiting[project.id] ?? 0 > 0 { return 0 }
            if leads[project.id]?.recent(at: now) == true { return 1 }
            return active(project) ? 2 : 3
        }
        func time(_ project: ProjectState) -> Date {
            max(project.activity?.lastActive.flatMap(millisecondDate) ?? .distantPast, leads[project.id]?.lastActive ?? .distantPast)
        }
        let open = projects.filter { $0.archived != true }
        let recent = open.sorted { lhs, rhs in
            let (left, right) = (group(lhs), group(rhs))
            if left != right { return left < right }
            let (leftTime, rightTime) = (time(lhs), time(rhs))
            if leftTime != rightTime { return leftTime > rightTime }
            return lhs.displayName.localizedStandardCompare(rhs.displayName) == .orderedAscending
        }
        let shown = Array(recent.prefix(limit))
        let rest = projects.filter { project in !shown.contains { $0.id == project.id } }
            .sorted { $0.displayName.localizedStandardCompare($1.displayName) == .orderedAscending }
        return ProjectTiles(shown: shown, more: rest)
    }

    static func arranged(_ projects: [ProjectState], limit: Int, order: [String], pinned: Set<String>) -> ProjectTiles {
        let place = Dictionary(order.enumerated().map { ($1, $0) }, uniquingKeysWith: { first, _ in first })
        let sorted = projects.sorted { lhs, rhs in
            let (leftPin, rightPin) = (pinned.contains(lhs.id), pinned.contains(rhs.id))
            if leftPin != rightPin { return leftPin }
            let (left, right) = (place[lhs.id] ?? Int.max, place[rhs.id] ?? Int.max)
            if left != right { return left < right }
            return lhs.displayName.localizedStandardCompare(rhs.displayName) == .orderedAscending
        }
        let shown = Array(sorted.filter { $0.archived != true }.prefix(max(0, limit)))
        let held = Set(shown.map(\.id))
        return ProjectTiles(shown: shown, more: sorted.filter { !held.contains($0.id) })
    }

    static func moving(_ id: String, before target: String, in order: [String]) -> [String] {
        guard id != target, order.contains(target) else { return order }
        var next = order.filter { $0 != id }
        next.insert(id, at: next.firstIndex(of: target) ?? next.count)
        return next
    }

    static func colourID(_ projectID: String, owner hostID: String) -> String {
        hostID.isEmpty || projectID.isEmpty || projectID.contains("::") || projectID.hasPrefix("repo:") || projectID.hasPrefix("shared:") ? projectID : hostID + "::" + projectID
    }

    static func colour(_ projectID: String) -> Int {
        var hash: UInt32 = 2_166_136_261
        for byte in projectID.utf8 { hash = (hash ^ UInt32(byte)) &* 16_777_619 }
        return Int(hash % UInt32(PaletteTokens.projects.count))
    }

    static func letter(_ name: String) -> String {
        name.first(where: { $0.isLetter || $0.isNumber }).map { String($0).uppercased() } ?? "·"
    }
}
