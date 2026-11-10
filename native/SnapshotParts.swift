import Foundation

struct SnapshotBase: Decodable {
    let updatedAt: String
    let eventCursor: UInt64?
    let hostEpoch: String?
    let catalogSeq: UInt64?
    let daemon: DaemonState
    let surfaces: [SurfaceState]
    let queue: [QueueState]
    let channels: [ChannelState]
    let relays: [RelayState]
    let codexRecentHours: Int
    var busyDelivery: String? = nil
    var rekeyed: [String: String]? = nil
    var signin: [SigninConsumerState]? = nil
    var person: PersonState? = nil
    var projects: [ProjectState]? = nil
    var leadReplacements: [LeadReplacement]? = nil
    var decisionsRev: String? = nil
}

struct ScopeSessions {
    var sessions: [SessionState]
    var hostEpoch: String?
    var catalogSeq: UInt64?
    var view: ScopePage.View? = nil
}

struct ScopePage: Decodable {
    struct Count: Decodable, Equatable {
        let families: Int
        let sessions: Int
    }

    struct Surface: Decodable, Equatable {
        let surface: String
        let counts: [String: Count]
    }

    struct View: Decodable, Equatable {
        var counts: [String: Count]
        let surfaces: [Surface]
        var refreshAt: String? = nil
        var projects: [ViewProject]
        var families: [ViewFamily]
    }

    let sessions: [SessionState]
    var nextCursor: String? = nil
    var hostEpoch: String? = nil
    var catalogSeq: UInt64? = nil
    var view: View? = nil
}

private struct ProjectActivityPage: Decodable {
    let projects: [ProjectActivity]
}

extension AgenthailAPI {
    static let snapshotProtocol = 2

    func snapshotBase(fresh: Bool = false, timeout: TimeInterval = 25) async throws -> SnapshotBase {
        try await taggedGet("/api/v1/snapshot?sessions=none" + (fresh ? "&fresh=1" : ""), key: "base", fresh: fresh, timeout: timeout)
    }

    func snapshotBaseAndCurrent(fresh: Bool = false, timeout: TimeInterval = 25) async throws -> (SnapshotBase, ScopeSessions) {
        for _ in 0..<3 {
            async let base = snapshotBase(fresh: fresh, timeout: timeout)
            async let current = scopeSessions("current", timeout: timeout)
            let read = try await (base, current)
            if read.0.hostEpoch == read.1.hostEpoch { return read }
        }
        throw AgenthailAPIError.unavailable("The computer restarted while Agenthail was reading it. Trying again.")
    }

    func projectActivity(fresh: Bool = false, timeout: TimeInterval = 25) async throws -> [ProjectActivity] {
        let page: ProjectActivityPage = try await taggedGet("/api/v1/projects/activity", key: "activity", fresh: fresh, timeout: timeout)
        return page.projects
    }

    // A session that moves between pages can appear twice; the later copy wins.
    func scopeSessions(_ scope: String, timeout: TimeInterval = 25) async throws -> ScopeSessions {
        var result = ScopeSessions(sessions: [])
        var seen: [String: Int] = [:]
        var cursor: String?
        repeat {
            let page = try await scopePage(ScopeRequest(scope: scope), cursor: cursor, limit: 200, timeout: timeout)
            for session in page.sessions {
                if let index = seen[session.id] { result.sessions[index] = session } else {
                    seen[session.id] = result.sessions.count
                    result.sessions.append(session)
                }
            }
            result.hostEpoch = result.hostEpoch ?? page.hostEpoch
            result.view = result.view?.merging(page.view) ?? page.view
            if let seq = page.catalogSeq { result.catalogSeq = min(result.catalogSeq ?? seq, seq) }
            cursor = page.nextCursor
        } while cursor != nil
        return result
    }

    func scopePage(_ request: ScopeRequest, cursor: String? = nil, limit: Int = 50, timeout: TimeInterval = 25) async throws -> ScopePage {
        var components = URLComponents()
        components.path = "/api/v1/snapshot"
        components.queryItems = [URLQueryItem(name: "scope", value: request.scope), URLQueryItem(name: "members", value: "1"), URLQueryItem(name: "limit", value: String(limit))]
        for (name, value) in [("projectId", request.projectID), ("surface", request.surface), ("status", request.status), ("q", request.query)] {
            if let value, !value.isEmpty { components.queryItems?.append(URLQueryItem(name: name, value: value)) }
        }
        if let cursor { components.queryItems?.append(URLQueryItem(name: "cursor", value: cursor)) }
        guard let path = components.string else { throw AgenthailAPIError.invalidResponse }
        var request = authorizedRequest(path: path)
        request.timeoutInterval = timeout
        let (data, response) = try await session.data(for: request)
        try validate(response: response, data: data)
        return try JSONDecoder().decode(ScopePage.self, from: data)
    }

    private func taggedGet<T: Decodable>(_ path: String, key: String, fresh: Bool, timeout: TimeInterval) async throws -> T {
        var request = authorizedRequest(path: path)
        request.timeoutInterval = timeout
        let held = fresh ? nil : tagged.held(key)
        if let held { request.setValue(held.tag, forHTTPHeaderField: "If-None-Match") }
        let (data, response) = try await session.data(for: request)
        let http = response as? HTTPURLResponse
        if let held, http?.statusCode == 304 { return try JSONDecoder().decode(T.self, from: held.data) }
        try validate(response: response, data: data)
        let value = try JSONDecoder().decode(T.self, from: data)
        tagged.store(key, tag: http?.value(forHTTPHeaderField: "ETag"), data: data)
        return value
    }
}

extension DashboardSnapshot {
    // The stream resumes from the older read so nothing between the two is missed.
    init(base: SnapshotBase, current: ScopeSessions, activity: [ProjectActivity], history: [HistoryState] = []) {
        let byID = Dictionary(activity.map { ($0.id, $0) }, uniquingKeysWith: { first, _ in first })
        let catalogSeq = [base.catalogSeq, current.catalogSeq].compactMap { $0 }.min()
        self.init(updatedAt: base.updatedAt, eventCursor: base.eventCursor, hostEpoch: base.hostEpoch, catalogSeq: catalogSeq, daemon: base.daemon, surfaces: base.surfaces,
                  sessions: current.sessions, totalSessions: current.sessions.count, queue: base.queue, channels: base.channels, relays: base.relays, history: history,
                  codexRecentHours: base.codexRecentHours, busyDelivery: base.busyDelivery,
                  rekeyed: base.rekeyed, signin: base.signin, person: base.person,
                  projects: base.projects?.map { project in
                      var project = project
                      project.activity = byID[project.id]
                      return project
                  }, leadReplacements: base.leadReplacements, decisionsRev: base.decisionsRev)
    }
}
