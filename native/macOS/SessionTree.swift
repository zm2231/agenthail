import Foundation

enum SessionFilter: String, CaseIterable, Identifiable {
    case running = "Running"
    case recent = "Recent"
    case all = "All"

    var id: String { rawValue }
}

struct SessionTree: Equatable {
    struct Project: Identifiable, Equatable {
        let id: String
        let name: String
        var checkouts: [Checkout]

        var sessionCount: Int { checkouts.reduce(0) { $0 + $1.families.count } }

        func limited(to limit: Int, keeping selectedID: String?) -> Project {
            let ordered = SessionTree.newestFirst(checkouts.flatMap(\.families), by: SessionTree.activity)
            var visible = Set(ordered.prefix(limit).map(\.id))
            if let selected = ordered.first(where: { $0.contains(selectedID) }) { visible.insert(selected.id) }
            var copy = self
            copy.checkouts = checkouts.compactMap { checkout in
                var trimmed = checkout
                trimmed.families = checkout.families.filter { visible.contains($0.id) }
                return trimmed.families.isEmpty ? nil : trimmed
            }
            return copy
        }
    }

    static let collapsedSessionLimit = 5

    struct Checkout: Identifiable, Equatable {
        let id: String
        let label: String
        let branchLabel: String?
        let isMain: Bool
        let dirty: Bool
        var families: [SessionFamily]

        var sessions: [SessionState] { families.map(\.root) }
    }

    static let recentWindow: TimeInterval = 24 * 60 * 60

    let projects: [Project]
    let needsYou: [SessionState]
    let counts: [SessionFilter: Int]

    static func build(_ sessions: [SessionState], filter: SessionFilter, attentionSessionIDs: Set<String>, now: Date) -> SessionTree {
        let families = SessionFamilies.build(sessions)
        var counts: [SessionFilter: Int] = [:]
        for candidate in SessionFilter.allCases {
            counts[candidate] = families.filter { includes($0, in: candidate, now: now) }.count
        }
        let visible = newestFirst(families.filter { includes($0, in: filter, now: now) }, by: activity)
        var projects: [Project] = []
        for family in visible {
            let session = family.root
            let projectID = session.hostProject?.id ?? session.cwd ?? "unknown"
            if !projects.contains(where: { $0.id == projectID }) {
                projects.append(Project(id: projectID, name: projectName(session), checkouts: []))
            }
            let projectIndex = projects.firstIndex { $0.id == projectID }!
            let checkoutID = session.checkout?.id ?? session.checkout?.path ?? session.cwd ?? projectID
            if !projects[projectIndex].checkouts.contains(where: { $0.id == checkoutID }) {
                projects[projectIndex].checkouts.append(Checkout(id: checkoutID, label: checkoutLabel(session), branchLabel: branchLabel(session), isMain: session.checkout?.isMain ?? true, dirty: session.checkout?.dirty ?? false, families: []))
            }
            let checkoutIndex = projects[projectIndex].checkouts.firstIndex { $0.id == checkoutID }!
            projects[projectIndex].checkouts[checkoutIndex].families.append(family)
        }
        return SessionTree(projects: projects, needsYou: needsYou(sessions, attentionSessionIDs: attentionSessionIDs), counts: counts)
    }

    static func needsYou(_ sessions: [SessionState], attentionSessionIDs: Set<String>) -> [SessionState] {
        newestFirst(sessions.filter { attentionSessionIDs.contains($0.id) })
    }

    static func newestFirst(_ sessions: [SessionState]) -> [SessionState] {
        newestFirst(sessions, by: activity)
    }

    /// Sorts newest first, reading each item's activity once rather than in
    /// every comparison.
    static func newestFirst<Item>(_ items: [Item], by activity: (Item) -> Date) -> [Item] {
        items.map { (item: $0, activity: activity($0)) }.sorted { $0.activity > $1.activity }.map(\.item)
    }

    static func includes(_ session: SessionState, in filter: SessionFilter, now: Date) -> Bool {
        includes(session, in: filter, activity: activity(session), now: now)
    }

    private static func includes(_ session: SessionState, in filter: SessionFilter, activity: Date, now: Date) -> Bool {
        switch filter {
        case .running:
            return session.isWorking
        case .recent:
            return session.isWorking || session.current || now.timeIntervalSince(activity) <= recentWindow
        case .all:
            return true
        }
    }

    static func includes(_ family: SessionFamily, in filter: SessionFilter, now: Date) -> Bool {
        switch filter {
        case .running:
            return family.isWorking
        case .recent, .all:
            return family.isWorking || family.sessions.contains { includes($0, in: filter, now: now) }
        }
    }

    static func activity(_ family: SessionFamily) -> Date {
        family.sessions.map(activity).max() ?? .distantPast
    }

    static func activity(_ session: SessionState) -> Date {
        guard let raw = session.lastActive else { return .distantPast }
        return parseTimestamp(raw) ?? .distantPast
    }

    static func parseTimestamp(_ raw: String) -> Date? {
        if let date = try? fractionalTimestamp.parse(raw) { return date }
        if let date = try? wholeSecondTimestamp.parse(raw) { return date }
        return sqliteTimestamp.date(from: raw)
    }

    private static let fractionalTimestamp = Date.ISO8601FormatStyle(includingFractionalSeconds: true)
    private static let wholeSecondTimestamp = Date.ISO8601FormatStyle()

    private static let sqliteTimestamp: DateFormatter = {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = TimeZone(identifier: "UTC")
        formatter.dateFormat = "yyyy-MM-dd HH:mm:ss"
        return formatter
    }()

    private static func projectName(_ session: SessionState) -> String {
        if let name = session.hostProject?.displayName, !name.isEmpty { return name }
        if let cwd = session.cwd, !cwd.isEmpty { return URL(fileURLWithPath: cwd).lastPathComponent }
        return "Other"
    }

    private static func checkoutLabel(_ session: SessionState) -> String {
        branchLabel(session) ?? session.checkout?.path ?? session.cwd ?? "unknown checkout"
    }

    private static func branchLabel(_ session: SessionState) -> String? {
        if let branch = session.checkout?.branch, !branch.isEmpty { return branch }
        if let head = session.checkout?.detachedHead, !head.isEmpty { return "detached at \(head.prefix(7))" }
        return nil
    }
}
