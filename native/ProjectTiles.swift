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
            self.init(lastActive: lastActive.flatMap(TimestampParser.date), active: waiting || currentReason == "working" || currentReason == "queued")
        }

        init(_ session: SessionState) {
            self.init(currentReason: session.currentReason, waiting: session.waitingOn != nil, lastActive: session.lastActive)
        }

        func recent(at now: Date) -> Bool {
            active || lastActive.map { now.timeIntervalSince($0) <= Self.recentWindow } == true
        }
    }

    static func order(_ projects: [ProjectState], limit: Int, waiting: [String: Int] = [:], leads: [String: Lead] = [:], now: Date = Date()) -> ProjectTiles {
        func group(_ project: ProjectState) -> Int {
            if waiting[project.id] ?? 0 > 0 { return 0 }
            return leads[project.id]?.recent(at: now) == true ? 1 : 2
        }
        func time(_ project: ProjectState) -> Date {
            max(project.activity?.lastActive.flatMap(TimestampParser.date) ?? .distantPast, leads[project.id]?.lastActive ?? .distantPast)
        }
        let open = projects.filter { $0.archived != true && ($0.activity?.sessionCount ?? 0 > 0 || !$0.leads.isEmpty || waiting[$0.id] ?? 0 > 0) }
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

    static func colourID(_ projectID: String, owner hostID: String) -> String {
        hostID.isEmpty || projectID.isEmpty || projectID.contains("::") || projectID.hasPrefix("repo:") ? projectID : hostID + "::" + projectID
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
