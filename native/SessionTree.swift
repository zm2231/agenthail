import Foundation

enum SessionFilter: String, CaseIterable, Identifiable {
    case running = "Running"
    case recent = "Recent"
    case all = "All"
    case archived = "Archived"

    var id: String { rawValue }

    var viewScope: String {
        switch self {
        case .running: "running"
        case .recent: "current"
        case .all: "all"
        case .archived: "archived"
        }
    }
}

typealias SessionStatusFilter = SessionDisplayKind

struct SessionRefinement: Equatable {
    var status: SessionStatusFilter?
    var surface: String?

    var isActive: Bool { status != nil || surface != nil }

    static func surfaceLabel(_ surface: String) -> String {
        surface.prefix(1).uppercased() + surface.dropFirst()
    }
}

/// The session list as the daemon orders it: projects, checkouts and families
/// in view order, joined with the rows the client holds.
struct SessionTree: Equatable {
    struct Project: Identifiable, Equatable {
        let id: String
        let name: String
        /// The daemon's project, or nil for the group of sessions without one.
        let identity: ProjectIdentity?
        var checkouts: [Checkout]
        var collapsed: Set<String> = []
        var counts = ScopePage.Count(families: 0, sessions: 0)

        var sessionCount: Int { checkouts.reduce(0) { $0 + $1.families.count } }
        var familyTotal: Int { max(counts.families, sessionCount) }
        var sessionCountLabel: String { SessionTree.sessionCountLabel(counts.sessions) }
        var archived: Bool { identity?.archived == true }
        /// The other hosts with a checkout, in list order.
        var hosts: [String] { HostNames.ordered(checkouts.compactMap(\.host)) }
        var hostLabel: String? { hosts.isEmpty ? nil : hosts.joined(separator: ", ") }
        var spansHosts: Bool { Set(checkouts.map(\.host?.id)).count > 1 }

        func hostLabel(of checkout: Checkout) -> String? {
            checkout.host.flatMap { HostNames.labels(checkouts.compactMap(\.host))[$0.id] }
        }

        func shown(expanded: Bool, keeping selectedID: String?) -> Project {
            expanded ? self : collapsed(keeping: selectedID)
        }

        func moreLabel(expanded: Bool, keeping selectedID: String?) -> String? {
            let count = shown(expanded: expanded, keeping: selectedID).sessionCount
            guard familyTotal > count || expanded && familyTotal > collapsed.count else { return nil }
            return expanded ? "Show less" : "Show \(familyTotal - count) more"
        }

        /// The daemon's collapsed families, and the family holding the selection in its place.
        func collapsed(keeping selectedID: String?) -> Project {
            var copy = self
            copy.checkouts = checkouts.compactMap { checkout in
                var trimmed = checkout
                trimmed.families = checkout.families.filter { collapsed.contains($0.id) || $0.contains(selectedID) }
                return trimmed.families.isEmpty ? nil : trimmed
            }
            return copy
        }
    }

    static func sessionCountLabel(_ count: Int) -> String {
        count == 1 ? "1 session" : "\(count) sessions"
    }

    struct Checkout: Identifiable, Equatable {
        let id: String
        /// The host the checkout is on, nil on this host.
        let host: SessionHost?
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
    var flat: [SessionFamily] = []

    static let noProjectID = ""
    static let empty = SessionTree(projects: [], needsYou: [], attention: [:], counts: [:])

    static func parseTimestamp(_ raw: String) -> Date? {
        TimestampParser.date(raw)
    }

    static func checkoutLabel(_ session: SessionState) -> String {
        branchLabel(session) ?? session.checkout?.path ?? session.cwd ?? "unknown checkout"
    }

    static func branchLabel(_ session: SessionState) -> String? {
        if let branch = session.checkout?.branch, !branch.isEmpty { return branch }
        if let head = session.checkout?.detachedHead, !head.isEmpty { return "detached at \(head.prefix(7))" }
        return nil
    }
}
