import Foundation

// Mirrors internal/daemon/dashboard/js/project-now.js; scripts/fixtures/project-now-vectors.json holds both to the same answers.
struct ProjectNow: Equatable {
    struct Group: Equatable, Identifiable {
        let id: String
        let title: String
        var sessions: [SessionState]
    }

    private(set) var groups: [Group] = []

    var count: Int { groups.reduce(0) { $0 + $1.sessions.count } }
    var showsGroups: Bool { groups.count > 1 }

    init(_ sessions: [SessionState] = [], computers: Int = 1, localName: String = "this computer") {
        let ordered = sessions.sorted(by: Self.before)
        var byID: [String: [SessionState]] = [:]
        var keys: [String] = []
        for session in ordered {
            let key = Self.key(session)
            if byID[key] == nil { keys.append(key) }
            byID[key, default: []].append(session)
        }
        groups = keys.map { key in
            let rows = byID[key] ?? []
            return Group(id: key, title: Self.title(rows, computers: computers, localName: localName), sessions: rows)
        }
    }

    private static func rank(_ session: SessionState) -> Int {
        if unreachable(session) { return 3 }
        if session.waitingOn != nil { return 0 }
        return ProjectContents.running(session) ? 1 : 2
    }

    private static func unreachable(_ session: SessionState) -> Bool {
        SessionDisplayKind.downHostStates.contains(session.host?.state ?? "")
    }

    private static func before(_ left: SessionState, _ right: SessionState) -> Bool {
        let (leftRank, rightRank) = (rank(left), rank(right))
        if leftRank != rightRank { return leftRank < rightRank }
        let (leftActive, rightActive) = (active(left), active(right))
        if leftActive != rightActive { return leftActive > rightActive }
        return left.id < right.id
    }

    private static func active(_ session: SessionState) -> Date {
        session.lastActive.flatMap(SessionTree.parseTimestamp) ?? .distantPast
    }

    private static func key(_ session: SessionState) -> String {
        session.checkout?.id ?? "\(session.host?.id ?? "")|\(session.cwd ?? "")"
    }

    private static func title(_ rows: [SessionState], computers: Int, localName: String) -> String {
        let first = rows[0]
        let unreachable = rows.allSatisfy(unreachable)
        var parts = [place(first)]
        if computers > 1 {
            let local = first.host == nil || first.host?.state == "local"
            parts.append(local ? localName : HostNames.short(first.host?.name ?? ""))
        }
        if unreachable { parts.append("unreachable") }
        else if rows.contains(where: { $0.checkout?.dirty == true }) { parts.append("modified") }
        return parts.joined(separator: " · ")
    }

    private static func place(_ session: SessionState) -> String {
        if let branch = session.checkout?.branch, !branch.isEmpty { return branch }
        if let head = session.checkout?.detachedHead, !head.isEmpty { return String(head.prefix(7)) }
        let path = session.checkout?.path ?? session.cwd ?? ""
        return path.isEmpty ? "No folder" : (path as NSString).lastPathComponent
    }
}
