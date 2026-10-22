import Foundation

/// One computer the phone is paired with, as its connection last saw it.
struct PairedHostView: Equatable {
    let id: String
    let name: String
    let reachable: Bool
    let snapshot: DashboardSnapshot?

    static func == (lhs: PairedHostView, rhs: PairedHostView) -> Bool {
        lhs.id == rhs.id && lhs.name == rhs.name && lhs.reachable == rhs.reachable && lhs.snapshot?.updatedAt == rhs.snapshot?.updatedAt
    }
}

/// Where an action on a session goes: the paired computer to send it to and
/// the id that computer knows the session by.
struct SessionRoute: Equatable {
    let hostID: String
    let sessionID: String
}

/// The sessions of every paired computer as one list. A session's id is
/// `<owner>::<session>` whether its row came from the owner or from another
/// computer that mirrors it, so the copies share one id. The owner's own row
/// wins while the owner is reachable; otherwise a reachable mirror's row
/// does. A session is unreachable only when no computer that serves it is.
struct HostCatalog {
    static let separator = "::"

    private(set) var sessions: [SessionState] = []
    private(set) var attention: [AttentionState] = []
    private(set) var deliveryProblems: [DeliveryProblem] = []
    private var routesByID: [String: [SessionRoute]] = [:]

    static func qualify(_ hostID: String, _ sessionID: String) -> String {
        sessionID.contains(separator) ? sessionID : hostID + separator + sessionID
    }

    static func owner(of sessionID: String) -> String? {
        sessionID.range(of: separator).map { String(sessionID[..<$0.lowerBound]) }
    }

    init() {}

    init(hosts: [PairedHostView]) {
        struct Candidate {
            var row: SessionState
            let route: SessionRoute
            let reachable: Bool
            let direct: Bool
        }
        let names = Dictionary(hosts.map { ($0.id, $0.name) }, uniquingKeysWith: { first, _ in first })
        var order: [String] = []
        var candidates: [String: [Candidate]] = [:]
        for host in hosts {
            guard let snapshot = host.snapshot else { continue }
            for row in snapshot.sessions {
                let local = row.host == nil || row.host?.state == "local"
                let owner = local ? host.id : (row.host?.id ?? Self.owner(of: row.id) ?? host.id)
                let id = Self.qualify(owner, row.id)
                var merged = row
                merged.id = id
                if local { merged = Self.qualifyFamily(merged, host: host.id) }
                let ownerName = names[owner] ?? row.host?.name ?? owner
                merged.host = SessionHost(id: owner, name: ownerName, state: host.reachable ? "connected" : "unreachable")
                if candidates[id] == nil { order.append(id) }
                candidates[id, default: []].append(Candidate(row: merged, route: SessionRoute(hostID: host.id, sessionID: row.id), reachable: host.reachable, direct: local))
            }
            attention += snapshot.attention.map { item in
                AttentionState(id: item.id, sessionId: Self.qualify(host.id, item.sessionId), target: item.target, queueId: item.queueId, reason: item.reason, requestedAction: item.requestedAction, createdAt: item.createdAt)
            }
            deliveryProblems += (snapshot.deliveryProblems ?? []).map { problem in
                DeliveryProblem(deliveryId: problem.deliveryId, sessionId: Self.qualify(host.id, problem.sessionId), sourceSessionId: problem.sourceSessionId.map { Self.qualify(host.id, $0) }, attentionSessionId: problem.attentionSessionId.map { Self.qualify(host.id, $0) }, message: problem.message, reason: problem.reason, status: problem.status, at: problem.at, hostID: host.id)
            }
        }
        for id in order {
            let all = candidates[id] ?? []
            let ranked = all.sorted { rank($0) > rank($1) }
            func rank(_ candidate: Candidate) -> Int { (candidate.reachable ? 2 : 0) + (candidate.direct ? 1 : 0) }
            guard var chosen = ranked.first else { continue }
            if !chosen.reachable {
                let ownerName = chosen.row.host?.name ?? Self.owner(of: id) ?? id
                if (chosen.row.unavailableReason ?? "").isEmpty {
                    chosen.row.unavailableReason = "\(ownerName) can't be reached"
                }
            }
            sessions.append(chosen.row)
            routesByID[id] = ranked.filter(\.reachable).map(\.route)
        }
    }

    /// The routes that can reach a session, best first: its owner, then any
    /// reachable computer that mirrors it.
    func routes(for sessionID: String) -> [SessionRoute] { routesByID[sessionID] ?? [] }

    func route(for sessionID: String) -> SessionRoute? { routes(for: sessionID).first }

    private static func qualifyFamily(_ row: SessionState, host: String) -> SessionState {
        var row = row
        if let subagent = row.subagent {
            row.subagent = SubagentIdentity(parentId: qualify(host, subagent.parentId), rootId: qualify(host, subagent.rootId), depth: subagent.depth, nickname: subagent.nickname, role: subagent.role)
        }
        if let project = row.project {
            row.project = ProjectIdentity(id: qualify(host, project.id), displayName: project.displayName, rootPath: project.rootPath, kind: project.kind, archived: project.archived)
        }
        return row
    }
}
