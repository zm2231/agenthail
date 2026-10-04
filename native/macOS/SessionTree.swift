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

        var sessionCount: Int { checkouts.reduce(0) { $0 + $1.sessions.count } }

        func limited(to limit: Int, keeping selectedID: String?) -> Project {
            let ordered = checkouts.flatMap(\.sessions).sorted { SessionTree.activity($0) > SessionTree.activity($1) }
            var visible = Set(ordered.prefix(limit).map(\.id))
            if let selectedID, ordered.contains(where: { $0.id == selectedID }) { visible.insert(selectedID) }
            var copy = self
            copy.checkouts = checkouts.compactMap { checkout in
                var trimmed = checkout
                trimmed.sessions = checkout.sessions.filter { visible.contains($0.id) }
                return trimmed.sessions.isEmpty ? nil : trimmed
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
        var sessions: [SessionState]
    }

    static let recentWindow: TimeInterval = 24 * 60 * 60

    let projects: [Project]
    let needsYou: [SessionState]
    let counts: [SessionFilter: Int]

    static func build(_ sessions: [SessionState], filter: SessionFilter, attentionSessionIDs: Set<String>, now: Date) -> SessionTree {
        var counts: [SessionFilter: Int] = [:]
        for candidate in SessionFilter.allCases {
            counts[candidate] = sessions.filter { includes($0, in: candidate, now: now) }.count
        }
        let visible = sessions
            .filter { includes($0, in: filter, now: now) }
            .sorted { activity($0) > activity($1) }
        var projects: [Project] = []
        for session in visible {
            let projectID = session.hostProject?.id ?? session.cwd ?? "unknown"
            if !projects.contains(where: { $0.id == projectID }) {
                projects.append(Project(id: projectID, name: projectName(session), checkouts: []))
            }
            let projectIndex = projects.firstIndex { $0.id == projectID }!
            let checkoutID = session.checkout?.id ?? session.checkout?.path ?? session.cwd ?? projectID
            if !projects[projectIndex].checkouts.contains(where: { $0.id == checkoutID }) {
                projects[projectIndex].checkouts.append(Checkout(id: checkoutID, label: checkoutLabel(session), branchLabel: branchLabel(session), isMain: session.checkout?.isMain ?? true, dirty: session.checkout?.dirty ?? false, sessions: []))
            }
            let checkoutIndex = projects[projectIndex].checkouts.firstIndex { $0.id == checkoutID }!
            projects[projectIndex].checkouts[checkoutIndex].sessions.append(session)
        }
        return SessionTree(projects: projects, needsYou: needsYou(sessions, attentionSessionIDs: attentionSessionIDs), counts: counts)
    }

    static func needsYou(_ sessions: [SessionState], attentionSessionIDs: Set<String>) -> [SessionState] {
        sessions
            .filter { attentionSessionIDs.contains($0.id) }
            .sorted { activity($0) > activity($1) }
    }

    static func includes(_ session: SessionState, in filter: SessionFilter, now: Date) -> Bool {
        switch filter {
        case .running:
            return session.isWorking
        case .recent:
            return session.isWorking || session.current || now.timeIntervalSince(activity(session)) <= recentWindow
        case .all:
            return true
        }
    }

    static func activity(_ session: SessionState) -> Date {
        guard let raw = session.lastActive else { return .distantPast }
        return parseTimestamp(raw) ?? .distantPast
    }

    static func parseTimestamp(_ raw: String) -> Date? {
        let fractional = ISO8601DateFormatter()
        fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let date = fractional.date(from: raw) { return date }
        if let date = ISO8601DateFormatter().date(from: raw) { return date }
        return sqliteTimestamp.date(from: raw)
    }

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
