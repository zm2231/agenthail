import Foundation

// Mirrors internal/daemon/dashboard/js/project-now.js; scripts/fixtures/project-now-vectors.json holds both to the same answers.
struct ProjectNow: Equatable {
    struct Group: Equatable, Identifiable {
        let id: String
        let title: String
        var sessions: [SessionState]
    }

    private static let knownSince: TimeInterval = 946_684_800

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
        let (leftKey, rightKey) = (sortKey(left, rank: leftRank), sortKey(right, rank: rightRank))
        if leftKey != rightKey { return leftKey < rightKey }
        return left.id < right.id
    }

    private static func sortKey(_ session: SessionState, rank: Int) -> Int64 {
        switch rank {
        case 0: return milliseconds(session.waitingOn?.since) ?? .max
        case 1: return -(milliseconds(session.registeredAt) ?? 0)
        default: return -(milliseconds(session.lastActive) ?? 0)
        }
    }

    private static func milliseconds(_ timestamp: String?) -> Int64? {
        guard let at = timestamp.map(toMilliseconds).flatMap(SessionTree.parseTimestamp), at.timeIntervalSince1970 >= knownSince else { return nil }
        return Int64((at.timeIntervalSince1970 * 1000).rounded())
    }

    private static func toMilliseconds(_ timestamp: String) -> String {
        guard let dot = timestamp.firstIndex(of: ".") else { return timestamp }
        let start = timestamp.index(after: dot)
        let end = timestamp[start...].firstIndex { !$0.isNumber } ?? timestamp.endIndex
        return String(timestamp[..<start] + timestamp[start..<end].prefix(3) + timestamp[end...])
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
        return path.isEmpty ? "No folder" : PathNames.last(path)
    }
}
