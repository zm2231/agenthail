import Foundation

enum SessionFilter: String, CaseIterable, Identifiable {
    case running = "Running"
    case recent = "Recent"
    case all = "All"
    case archived = "Archived"

    var id: String { rawValue }
}

enum SessionStatusFilter: String, CaseIterable, Identifiable {
    case busy
    case idle
    case offline
    case unavailable

    var id: String { rawValue }

    var label: String {
        switch self {
        case .busy: return "Working"
        case .idle: return "Idle"
        case .offline: return "Offline"
        case .unavailable: return "Unavailable"
        }
    }

    func matches(_ session: SessionState) -> Bool {
        switch self {
        case .unavailable: return ["unknown", "notLoaded"].contains(session.status)
        default: return session.status == rawValue
        }
    }
}

struct SessionRefinement: Equatable {
    var status: SessionStatusFilter?
    var surface: String?

    var isActive: Bool { status != nil || surface != nil }

    func matches(_ session: SessionState) -> Bool {
        (status?.matches(session) ?? true) && (surface.map { $0 == session.surface } ?? true)
    }

    func matches(_ family: SessionFamily) -> Bool {
        !isActive || family.sessions.contains(where: matches)
    }

    static func surfaces(_ sessions: [SessionState], configured: [SurfaceState]) -> [String] {
        Array(Set(sessions.map(\.surface) + configured.map(\.name))).sorted()
    }

    static func surfaceLabel(_ surface: String) -> String {
        surface.prefix(1).uppercased() + surface.dropFirst()
    }
}

struct SessionTree: Equatable {
    struct Project: Identifiable, Equatable {
        let id: String
        let name: String
        /// The daemon's project, or nil for the group of sessions without one.
        let identity: ProjectIdentity?
        /// The short name of the host that owns the project, nil on this host.
        let host: String?
        var checkouts: [Checkout]
        /// Families that are working, queued or need attention. Collapsing a
        /// project never hides them.
        var pinned: Set<String> = []

        var sessionCount: Int { checkouts.reduce(0) { $0 + $1.families.count } }
        var archived: Bool { identity?.archived == true }

        func limited(to limit: Int, keeping selectedID: String?) -> Project {
            let ordered = SessionTree.newestFirst(checkouts.flatMap(\.families), by: SessionTree.activity)
            var visible = Set(ordered.prefix(limit).map(\.id)).union(pinned)
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

    let projects: [Project]
    let needsYou: [SessionState]
    let attention: [String: SessionAttention]
    let counts: [SessionFilter: Int]

    static let noProjectID = ""

    static func build(_ sessions: [SessionState], filter: SessionFilter, refinement: SessionRefinement = SessionRefinement(), attention: [String: SessionAttention]) -> SessionTree {
        let families = SessionFamilies.build(sessions).filter(refinement.matches)
        var counts: [SessionFilter: Int] = [:]
        for candidate in SessionFilter.allCases {
            counts[candidate] = families.filter { includes($0, in: candidate) }.count
        }
        let visible = newestFirst(families.filter { includes($0, in: filter) }, by: activity)
        var projects: [Project] = []
        for family in visible {
            let session = family.root
            let projectID = session.project?.id ?? noProjectID + (session.remoteHostName.map { "@" + $0 } ?? "")
            if !projects.contains(where: { $0.id == projectID }) {
                projects.append(Project(id: projectID, name: session.project?.displayName ?? "No project", identity: session.project, host: session.shortRemoteHostName, checkouts: []))
            }
            let projectIndex = projects.firstIndex { $0.id == projectID }!
            if family.isWorking || family.sessions.contains(where: { $0.queueCount > 0 || attention[$0.id] != nil }) {
                projects[projectIndex].pinned.insert(family.id)
            }
            let checkoutID = session.checkout?.id ?? session.checkout?.path ?? session.cwd ?? projectID
            if !projects[projectIndex].checkouts.contains(where: { $0.id == checkoutID }) {
                projects[projectIndex].checkouts.append(Checkout(id: checkoutID, label: checkoutLabel(session), branchLabel: branchLabel(session), isMain: session.checkout?.isMain ?? true, dirty: session.checkout?.dirty ?? false, families: []))
            }
            let checkoutIndex = projects[projectIndex].checkouts.firstIndex { $0.id == checkoutID }!
            projects[projectIndex].checkouts[checkoutIndex].families.append(family)
        }
        projects = projects.filter { $0.identity != nil } + projects.filter { $0.identity == nil }
        return SessionTree(projects: projects, needsYou: needsYou(sessions, attention: attention), attention: attention, counts: counts)
    }

    /// Sessions that need the person: anything waiting on an answer first,
    /// then delivery decisions, newest first within each.
    static func needsYou(_ sessions: [SessionState], attention: [String: SessionAttention]) -> [SessionState] {
        let flagged = sessions.compactMap { session in attention[session.id].map { (session: session, kind: $0.kind) } }
        return SessionAttention.Kind.allCases.flatMap { kind in
            newestFirst(flagged.filter { $0.kind == kind }.map(\.session))
        }
    }

    static func newestFirst(_ sessions: [SessionState]) -> [SessionState] {
        newestFirst(sessions, by: activity)
    }

    /// Sorts newest first, reading each item's activity once rather than in
    /// every comparison.
    static func newestFirst<Item>(_ items: [Item], by activity: (Item) -> Date) -> [Item] {
        items.map { (item: $0, activity: activity($0)) }.sorted { $0.activity > $1.activity }.map(\.item)
    }

    /// Applies the daemon's scope rule to the flags it computed for the row,
    /// so every client lists and counts the same sessions.
    static func includes(_ session: SessionState, in filter: SessionFilter) -> Bool {
        let archived = session.archived == true
        switch filter {
        case .running: return !archived && (session.isWorking || (session.subagents?.working ?? 0) > 0)
        case .recent: return !archived && session.current
        case .all: return !archived
        case .archived: return archived
        }
    }

    static func includes(_ family: SessionFamily, in filter: SessionFilter) -> Bool {
        family.sessions.contains { includes($0, in: filter) }
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

    private static func checkoutLabel(_ session: SessionState) -> String {
        branchLabel(session) ?? session.checkout?.path ?? session.cwd ?? "unknown checkout"
    }

    private static func branchLabel(_ session: SessionState) -> String? {
        if let branch = session.checkout?.branch, !branch.isEmpty { return branch }
        if let head = session.checkout?.detachedHead, !head.isEmpty { return "detached at \(head.prefix(7))" }
        return nil
    }
}

/// Why a session needs the person, worded for the row that shows it. Only a
/// session that can still act is flagged: archived and unavailable sessions
/// never are, whatever their records say.
struct SessionAttention: Equatable {
    enum Kind: CaseIterable {
        /// The agent waits on an answer, an approval, or input.
        case request
        /// A message to this session failed and needs a decision.
        case incoming
        /// Messages this session sent were not delivered.
        case outgoing
    }

    let kind: Kind
    let reason: String

    static func build(sessions: [SessionState], attention: [AttentionState], deliveryProblems: [DeliveryProblem]) -> [String: SessionAttention] {
        let all = Dictionary(sessions.map { ($0.id, $0) }, uniquingKeysWith: { first, _ in first })
        let live = all.filter { canAct($0.value) }
        var result: [String: SessionAttention] = [:]
        let outgoing = Dictionary(grouping: deliveryProblems.filter { $0.attentionSessionId.map { live[$0] != nil } ?? false }, by: { $0.attentionSessionId! })
        for (id, problems) in outgoing {
            let targets = Set(problems.map(\.sessionId))
            let count = problems.count == 1 ? "1 message" : "\(problems.count) messages"
            let target = targets.count == 1 ? all[targets.first!].map { " to \($0.displayName)" } ?? "" : ""
            result[id] = SessionAttention(kind: .outgoing, reason: "\(count)\(target) not delivered")
        }
        for (id, items) in Dictionary(grouping: attention.filter { live[$0.sessionId] != nil }, by: \.sessionId) {
            let reasons = Set(items.map(\.reason))
            let reason: String
            if items.count == 1 {
                reason = items[0].reason
            } else if reasons.count == 1 {
                reason = "\(items.count) messages: \(reasons.first!)"
            } else {
                reason = "\(items.count) messages need a decision"
            }
            result[id] = SessionAttention(kind: .incoming, reason: reason)
        }
        for session in live.values {
            guard let waiting = session.waitingOn else { continue }
            result[session.id] = SessionAttention(kind: .request, reason: requestReason(waiting.kind))
        }
        return result
    }

    static func canAct(_ session: SessionState) -> Bool {
        session.archived != true && !session.isUnavailable && !["unknown", "notLoaded"].contains(session.status)
    }

    static func requestReason(_ kind: String) -> String {
        switch kind {
        case "question": return "Asks you a question"
        case "input": return "Needs your input"
        case "dialog": return "Has a dialog open in its terminal"
        default: return "Needs your approval"
        }
    }
}
