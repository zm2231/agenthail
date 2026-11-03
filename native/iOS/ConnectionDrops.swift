import Foundation
import Network

@MainActor
final class ConnectionDrops {
    static let shared = ConnectionDrops(defaults: .standard, watchesNetwork: true)
    static let defaultsKey = "connectionDrops"

    private let defaults: UserDefaults
    private let monitor: NWPathMonitor?
    private(set) var log: ConnectionDropLog
    var network: NetworkSnapshot?
    var now: () -> Date = Date.init
    private var uploading: Set<String> = []
    private var refusedAt: [String: Date] = [:]
    static let retryAfter: TimeInterval = 10 * 60

    init(defaults: UserDefaults, watchesNetwork: Bool) {
        self.defaults = defaults
        log = defaults.data(forKey: Self.defaultsKey).flatMap { try? JSONDecoder().decode(ConnectionDropLog.self, from: $0) } ?? ConnectionDropLog()
        monitor = watchesNetwork ? NWPathMonitor() : nil
        monitor?.pathUpdateHandler = { [weak self] path in
            let snapshot = Self.snapshot(path)
            Task { @MainActor in self?.network = snapshot }
        }
        monitor?.start(queue: DispatchQueue(label: "com.agenthail.connection-drops.path"))
    }

    nonisolated static func snapshot(_ path: NWPath) -> NetworkSnapshot {
        let interface: String
        if path.status != .satisfied { interface = "none" }
        else if path.usesInterfaceType(.wifi) { interface = "wifi" }
        else if path.usesInterfaceType(.cellular) { interface = "cellular" }
        else if path.usesInterfaceType(.wiredEthernet) { interface = "wired" }
        else { interface = "other" }
        let vpn = path.availableInterfaces.contains { $0.type == .other || $0.name.hasPrefix("utun") || $0.name.hasPrefix("ipsec") }
        return NetworkSnapshot(interface: interface, vpn: vpn, expensive: path.isExpensive, constrained: path.isConstrained)
    }

    func failed(host: String, via: String? = nil, path: String, error: String, elapsed: TimeInterval?, shown: ConnectionDrop.Shown) {
        log.fail(host: host, via: via, path: path, error: error, elapsed: elapsed, shown: shown, network: network, now: now()) {
            UUID().uuidString.lowercased().replacingOccurrences(of: "-", with: "")
        }
        save()
    }

    func recovered(host: String, via: String? = nil, by step: String) -> Bool {
        guard log.recover(host: host, via: via, by: step, now: now()) else { return false }
        save()
        return true
    }

    func peersConnected(via: String, _ connected: Set<String>) -> Bool {
        guard log.recoverRelays(via: via, connected: connected, now: now()) else { return false }
        save()
        return true
    }

    func foregrounded() {
        log.foregrounded(at: now())
        save()
    }

    func backgrounded() {
        log.backgrounded()
        save()
    }

    func stopped(host: String) {
        log.stopped(host: host)
        save()
    }

    func forget(host: String) {
        log.forget(host: host)
        save()
    }

    func upload(to computer: String, with api: AgenthailAPI) async {
        let pending = log.pending(for: computer)
        guard !pending.isEmpty, !uploading.contains(computer) else { return }
        if let refused = refusedAt[computer], now().timeIntervalSince(refused) < Self.retryAfter { return }
        uploading.insert(computer)
        defer { uploading.remove(computer) }
        guard (try? await api.postConnectionDrops(pending)) != nil else {
            refusedAt[computer] = now()
            return
        }
        refusedAt[computer] = nil
        log.uploaded(Set(pending.map(\.id)))
        log.bound(now: now())
        save()
    }

    private func save() {
        if let data = try? JSONEncoder().encode(log) { defaults.set(data, forKey: Self.defaultsKey) }
    }
}

extension AgenthailAPI {
    func postConnectionDrops(_ drops: [ConnectionDrop]) async throws {
        var request = authorizedRequest(path: "/api/v1/device/connection-drops")
        request.httpMethod = "POST"
        request.timeoutInterval = 15
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        let encoder = JSONEncoder()
        let style = Date.ISO8601FormatStyle(includingFractionalSeconds: true)
        encoder.dateEncodingStrategy = .custom { date, encoder in
            var container = encoder.singleValueContainer()
            try container.encode(date.formatted(style))
        }
        request.httpBody = try encoder.encode(["drops": drops])
        let (data, response) = try await session.data(for: request)
        try validate(response: response, data: data)
    }
}
