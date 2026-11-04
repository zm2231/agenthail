import Foundation

struct PairedHostView: Equatable {
    let id: String
    let name: String
    let reachable: Bool
    var holding = false
    let snapshot: DashboardSnapshot?

    var shownReachable: Bool { reachable || holding }

    static func == (lhs: PairedHostView, rhs: PairedHostView) -> Bool {
        lhs.id == rhs.id && lhs.name == rhs.name && lhs.reachable == rhs.reachable && lhs.holding == rhs.holding && lhs.snapshot?.updatedAt == rhs.snapshot?.updatedAt
    }
}

struct SessionRoute: Equatable {
    let hostID: String
    let sessionID: String
}

struct HostTarget: Equatable {
    let hostID: String
    let remote: String?

    /// The settings API is not forwarded to a computer reached through another.
    var takesSettings: Bool { remote == nil }
}

/// The sessions of every paired computer as one list. A session's id is
/// `<owner>::<session>` whether its row came from the owner or from another
/// computer that mirrors it, so the copies share one id. The owner's own row
/// wins while the owner is reachable; otherwise a reachable mirror's row
/// does. A session has no route only when no computer that serves it is
/// reachable; a mirror's row still shows the owner as down when it says so.
struct HostCatalog {
    static let separator = "::"

    private(set) var sessions: [SessionState] = []
    private(set) var attention: [AttentionState] = []
    private(set) var deliveryProblems: [DeliveryProblem] = []
    private(set) var leadReplacements: [LeadReplacement] = []
    private(set) var hosts: [HostHealth] = []
    private var routesByID: [String: [SessionRoute]] = [:]
    private var servers: [String: String] = [:]
    private var targets: [String: HostTarget] = [:]

    /// Names the same project on every computer, so it is never qualified.
    static let repositoryPrefix = "repo:"

    static func qualify(_ hostID: String, _ sessionID: String) -> String {
        hostID.isEmpty || sessionID.isEmpty || sessionID.contains(separator) || sessionID.hasPrefix(repositoryPrefix) ? sessionID : hostID + separator + sessionID
    }

    static func owner(of sessionID: String) -> String? {
        guard !sessionID.hasPrefix(repositoryPrefix), let range = sessionID.range(of: separator), range.lowerBound > sessionID.startIndex else { return nil }
        return String(sessionID[..<range.lowerBound])
    }

    static func merge(_ row: SessionState, servedBy hostID: String, ownerName: (String) -> String?, reachable: Bool) -> (row: SessionState, local: Bool) {
        let local = row.host == nil || row.host?.state == "local"
        let owner = local ? hostID : (row.host?.id ?? owner(of: row.id) ?? hostID)
        var merged = row
        merged.id = qualify(owner, row.id)
        if local { merged = qualifyFamily(merged, host: hostID) }
        let name = ownerName(owner) ?? row.host?.name ?? owner
        let ownerDown = !local && SessionDisplayKind.downHostStates.contains(row.host?.state ?? "")
        merged.host = SessionHost(id: owner, name: name, state: !reachable ? "unreachable" : ownerDown ? row.host?.state ?? "unreachable" : "connected")
        return (merged, local)
    }

    init() {}

    init(hosts paired: [PairedHostView]) {
        struct Candidate {
            var row: SessionState
            let route: SessionRoute
            let reachable: Bool
            let holding: Bool
            let direct: Bool
        }
        let names = Dictionary(paired.map { ($0.id, $0.name) }, uniquingKeysWith: { first, _ in first })
        var order: [String] = []
        var candidates: [String: [Candidate]] = [:]
        for host in paired {
            guard let snapshot = host.snapshot else { continue }
            for row in snapshot.sessions {
                let (merged, local) = Self.merge(row, servedBy: host.id, ownerName: { names[$0] }, reachable: host.shownReachable)
                if candidates[merged.id] == nil { order.append(merged.id) }
                candidates[merged.id, default: []].append(Candidate(row: merged, route: SessionRoute(hostID: host.id, sessionID: row.id), reachable: host.reachable, holding: host.holding, direct: local))
            }
            attention += snapshot.attention.map { item in
                AttentionState(id: item.id, sessionId: Self.qualify(host.id, item.sessionId), target: item.target, queueId: item.queueId, reason: item.reason, requestedAction: item.requestedAction, createdAt: item.createdAt)
            }
            deliveryProblems += (snapshot.deliveryProblems ?? []).map { problem in
                DeliveryProblem(deliveryId: problem.deliveryId, sessionId: Self.qualify(host.id, problem.sessionId), sourceSessionId: problem.sourceSessionId.map { Self.qualify(host.id, $0) }, attentionSessionId: problem.attentionSessionId.map { Self.qualify(host.id, $0) }, message: problem.message, reason: problem.reason, status: problem.status, at: problem.at, hostID: host.id)
            }
            leadReplacements += (snapshot.leadReplacements ?? []).map { item in
                LeadReplacement(id: item.id, projectId: item.projectId, name: item.name, sessionId: Self.qualify(host.id, item.sessionId), previousSessionId: Self.qualify(host.id, item.previousSessionId), changedBy: item.changedBy, reason: item.reason, at: item.at, hostID: host.id)
            }
        }
        for id in order {
            let all = candidates[id] ?? []
            let ranked = all.sorted { rank($0) > rank($1) }
            func rank(_ candidate: Candidate) -> Int { (candidate.reachable ? 4 : candidate.holding ? 2 : 0) + (candidate.direct ? 1 : 0) }
            guard var chosen = ranked.first else { continue }
            if !chosen.reachable && !chosen.holding {
                let ownerName = chosen.row.host?.name ?? Self.owner(of: id) ?? id
                if (chosen.row.unavailableReason ?? "").isEmpty {
                    chosen.row.unavailableReason = "\(ownerName) can't be reached"
                }
            }
            sessions.append(chosen.row)
            routesByID[id] = ranked.filter { $0.reachable || $0.holding }.map(\.route)
            servers[id] = chosen.route.hostID
        }
        for host in paired {
            hosts.append(HostHealth(id: host.id, name: host.name, state: host.shownReachable ? "connected" : "unreachable"))
            if host.reachable { targets[host.id] = HostTarget(hostID: host.id, remote: nil) }
        }
        for host in paired where host.reachable {
            for peer in host.snapshot?.daemon.hosts ?? [] where peer.state == "connected" && targets[peer.id] == nil {
                targets[peer.id] = HostTarget(hostID: host.id, remote: peer.id)
            }
        }
        for host in paired where host.holding && targets[host.id] == nil {
            targets[host.id] = HostTarget(hostID: host.id, remote: nil)
        }
        for host in paired where host.shownReachable {
            for peer in host.snapshot?.daemon.hosts ?? [] where names[peer.id] == nil {
                if let index = hosts.firstIndex(where: { $0.id == peer.id }) {
                    if peer.state == "connected" { hosts[index] = HostHealth(id: peer.id, name: hosts[index].name, state: "connected") }
                } else {
                    hosts.append(HostHealth(id: peer.id, name: peer.name, state: peer.state))
                }
            }
            for peer in host.snapshot?.daemon.hosts ?? [] where peer.state == "connected" && targets[peer.id] == nil {
                targets[peer.id] = HostTarget(hostID: host.id, remote: peer.id)
            }
        }
    }

    func routes(for sessionID: String) -> [SessionRoute] { routesByID[sessionID] ?? [] }

    /// The best route to a session. A session no paired computer lists, such
    /// as one a notification opened, goes to its owner by its id.
    func route(for sessionID: String) -> SessionRoute? {
        if let listed = routes(for: sessionID).first { return listed }
        guard let owner = Self.owner(of: sessionID), let target = target(forHost: owner) else { return nil }
        return target.remote == nil ? SessionRoute(hostID: owner, sessionID: String(sessionID.dropFirst(owner.count + Self.separator.count))) : SessionRoute(hostID: target.hostID, sessionID: sessionID)
    }

    func target(forHost hostID: String) -> HostTarget? { targets[hostID] }

    func server(of sessionID: String) -> String? { servers[sessionID] }

    private static func qualifyFamily(_ row: SessionState, host: String) -> SessionState {
        var row = row
        if let subagent = row.subagent {
            row.subagent = SubagentIdentity(parentId: qualify(host, subagent.parentId), rootId: qualify(host, subagent.rootId), depth: subagent.depth, nickname: subagent.nickname, role: subagent.role)
        }
        if let project = row.project {
            row.project = ProjectIdentity(id: qualify(host, project.id), displayName: project.displayName, rootPath: project.rootPath, kind: project.kind, archived: project.archived)
        }
        if let checkout = row.checkout, let id = checkout.id {
            row.checkout = CheckoutIdentity(id: qualify(host, id), path: checkout.path, branch: checkout.branch, detachedHead: checkout.detachedHead, isMain: checkout.isMain, dirty: checkout.dirty)
        }
        return row
    }
}
