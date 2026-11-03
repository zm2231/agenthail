import Foundation

struct NetworkSnapshot: Equatable, Sendable {
    var interface: String
    var vpn: Bool
    var expensive: Bool
    var constrained: Bool
}

struct ConnectionDrop: Codable, Equatable, Sendable {
    enum Shown: String, Codable, Sendable { case held, offline }

    let id: String
    let host: String
    let via: String?
    let path: String
    let startedAt: Date
    var recoveredAt: Date?
    var recoveredBy: String?
    let firstError: String
    var lastError: String
    let elapsedMs: Int?
    var attempts: Int
    var shown: Shown
    let sinceForegroundMs: Int?
    var wentBackground: Bool
    let network: String?
    let vpn: Bool
    let expensive: Bool
    let constrained: Bool

    var computer: String { via ?? host }
}

struct ConnectionDropLog: Codable, Equatable {
    static let kept = 50
    static let maxAge: TimeInterval = 7 * 24 * 60 * 60
    static let steadyAfter: TimeInterval = 10 * 60

    private(set) var open: [String: ConnectionDrop] = [:]
    private(set) var closed: [ConnectionDrop] = []
    private(set) var activeSince: Date?

    private static func key(_ host: String, _ via: String?) -> String { host + "|" + (via ?? "") }

    mutating func fail(host: String, via: String? = nil, path: String, error: String, elapsed: TimeInterval?, shown: ConnectionDrop.Shown,
                       network: NetworkSnapshot?, now: Date, id: () -> String) {
        let key = Self.key(host, via)
        if var episode = open[key] {
            episode.attempts += 1
            episode.lastError = error
            if shown == .offline { episode.shown = .offline }
            open[key] = episode
            return
        }
        let sinceForeground = activeSince.map { now.timeIntervalSince($0) }.flatMap { $0 >= 0 && $0 < Self.steadyAfter ? Int($0 * 1000) : nil }
        open[key] = ConnectionDrop(id: id(), host: host, via: via, path: path, startedAt: now, recoveredAt: nil, recoveredBy: nil, firstError: error, lastError: error,
                                   elapsedMs: elapsed.map { Int(max(0, $0) * 1000) }, attempts: 1, shown: shown, sinceForegroundMs: sinceForeground,
                                   wentBackground: false, network: network?.interface, vpn: network?.vpn ?? false,
                                   expensive: network?.expensive ?? false, constrained: network?.constrained ?? false)
    }

    @discardableResult
    mutating func recover(host: String, via: String? = nil, by step: String, now: Date) -> Bool {
        guard var episode = open.removeValue(forKey: Self.key(host, via)) else { return false }
        episode.recoveredAt = max(now, episode.startedAt)
        episode.recoveredBy = step
        closed.append(episode)
        bound(now: now)
        return true
    }

    @discardableResult
    mutating func recoverRelays(via: String, connected: Set<String>, now: Date) -> Bool {
        let ready = open.values.filter { $0.via == via && connected.contains($0.host) }
        for episode in ready { recover(host: episode.host, via: via, by: "peer connected", now: now) }
        return !ready.isEmpty
    }

    mutating func foregrounded(at now: Date) { activeSince = now }

    mutating func backgrounded() {
        activeSince = nil
        for key in open.keys { open[key]?.wentBackground = true }
    }

    mutating func stopped(host: String) {
        open = open.filter { $0.value.computer != host }
    }

    mutating func forget(host: String) {
        stopped(host: host)
        closed.removeAll { $0.computer == host }
    }

    func pending(for computer: String) -> [ConnectionDrop] {
        Array(closed.filter { $0.computer == computer }.prefix(Self.kept))
    }

    mutating func uploaded(_ ids: Set<String>) {
        closed.removeAll { ids.contains($0.id) }
    }

    mutating func bound(now: Date) {
        closed.removeAll { now.timeIntervalSince($0.startedAt) > Self.maxAge }
        open = open.filter { now.timeIntervalSince($0.value.startedAt) <= Self.maxAge }
        for computer in Set(closed.map(\.computer)) {
            let mine = closed.filter { $0.computer == computer }
            guard mine.count > Self.kept else { continue }
            let dropped = Set(mine.sorted { $0.startedAt < $1.startedAt }.prefix(mine.count - Self.kept).map(\.id))
            closed.removeAll { dropped.contains($0.id) }
        }
    }

    static func code(_ error: Error) -> String {
        let raw: String
        if let api = error as? AgenthailAPIError {
            if case .request(let status, _) = api { raw = "http.\(status)" } else { raw = String(String(describing: api).prefix { $0 != "(" }) }
        } else if let url = error as? URLError {
            raw = "URLError.\(url.code.rawValue)"
        } else if error is CancellationError {
            raw = "cancelled"
        } else {
            let ns = error as NSError
            raw = "\(ns.domain).\(ns.code)"
        }
        let allowed = Set("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-")
        let cleaned = String(raw.filter { allowed.contains($0) }.prefix(64))
        return cleaned.isEmpty ? "unknown" : cleaned
    }
}
