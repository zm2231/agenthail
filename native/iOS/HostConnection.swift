import Foundation

@MainActor
protocol HostConnectionDelegate: AnyObject {
    func hostDidChange(_ host: HostConnection)
    func hostEventStreamConnected(_ host: HostConnection) async
    func nextStateRequest() -> UInt64
    func host(_ host: HostConnection, rekeyed retired: [String: String])
    func host(_ host: HostConnection, removedSession id: String, unlisted: Bool)
    func host(_ host: HostConnection, receivedEventFor entity: String)
    func hostRefreshed(_ host: HostConnection) async
    func hostConnected(_ host: HostConnection)
    func hostAppearanceChanged(_ host: HostConnection)
}

@MainActor
final class HostConnection {
    let id: String
    private(set) var credential: PairedHostCredential
    let api: AgenthailAPI
    let isPreview: Bool
    private(set) var snapshot: DashboardSnapshot?
    private(set) var reachable = false
    private(set) var reconnecting = false
    private(set) var connectionError: String?
    private(set) var pushRelayURL: URL?
    private(set) var build: AgenthailBuild?
    private(set) var catalogStreamCursor: UInt64 = 0
    private(set) var snapshotSeq: UInt64 = 0
    private var snapshotCoversEventStream = false
    private var stopped = false
    private var catalogHostEpoch: String?
    private var lastEventID: UInt64 = 0
    private var connectionTask: Task<Void, Never>?
    private var eventTask: Task<Void, Never>?
    private var eventRefreshTask: Task<Void, Never>?
    private var catalogStreamTask: Task<Void, Never>?
    weak var delegate: HostConnectionDelegate?

    init(credential: PairedHostCredential, session: URLSession) {
        id = credential.hostID
        self.credential = credential
        api = AgenthailAPI(baseURL: credential.endpoint, token: credential.token, session: session)
        isPreview = false
        pushRelayURL = KeychainStore.get(Self.relayAccount(credential.hostID)).flatMap(URL.init(string:))
    }

    init(id: String, name: String, api: AgenthailAPI) {
        self.id = id
        credential = PairedHostCredential(hostID: id, hostName: name, endpoint: URL(string: "https://preview.invalid")!, token: "preview", pairedAt: Date())
        self.api = api
        isPreview = true
        reachable = true
    }

    deinit {
        connectionTask?.cancel()
        eventTask?.cancel()
        eventRefreshTask?.cancel()
        catalogStreamTask?.cancel()
    }

    var name: String { snapshot?.daemon.host?.name ?? credential.hostName }

    var view: PairedHostView { PairedHostView(id: id, name: name, reachable: reachable, snapshot: snapshot) }

    static func relayAccount(_ hostID: String) -> String { "pushRelayURL:\(hostID)" }

    func connect() {
        guard !stopped else { return }
        connectionTask?.cancel()
        eventTask?.cancel()
        eventRefreshTask?.cancel()
        lastEventID = 0
        reconnecting = true
        delegate?.hostDidChange(self)
        connectionTask = Task {
            let backoff = EventRetryBackoff()
            while !Task.isCancelled, !stopped {
                do {
                    let version = try await api.version()
                    guard !Task.isCancelled, !stopped else { return }
                    build = version.build
                    if let value = version.pushRelayUrl, let url = URL(string: value) {
                        pushRelayURL = url
                        try? KeychainStore.set(value, account: Self.relayAccount(id))
                    }
                    if await refresh(fresh: true) {
                        guard !Task.isCancelled, !stopped else { return }
                        snapshotCoversEventStream = snapshot?.daemon.stale != true
                        startEvents()
                        delegate?.hostConnected(self)
                        return
                    }
                } catch {
                    guard !stopped else { return }
                    connectionError = error.localizedDescription
                    reachable = false
                    if let apiError = error as? AgenthailAPIError, case .incompatible = apiError {
                        reconnecting = false
                        delegate?.hostDidChange(self)
                        return
                    }
                }
                reconnecting = true
                delegate?.hostDidChange(self)
                let delay = backoff.nextDelay()
                try? await Task.sleep(for: .seconds(delay))
            }
        }
    }

    func stop() {
        stopped = true
        connectionTask?.cancel()
        eventTask?.cancel()
        eventRefreshTask?.cancel()
        catalogStreamTask?.cancel()
        reachable = false
        reconnecting = false
    }

    func setSnapshot(_ value: DashboardSnapshot?) {
        snapshot = value
        reachable = value != nil
        delegate?.hostDidChange(self)
    }

    func updateSnapshot(_ change: (inout DashboardSnapshot) -> Void) {
        guard var current = snapshot else { return }
        change(&current)
        snapshot = current
        delegate?.hostDidChange(self)
    }

    @discardableResult
    func refresh(fresh: Bool = false) async -> Bool {
        let seq = delegate?.nextStateRequest() ?? 0
        do {
            let loaded = try await api.snapshot(fresh: fresh)
            guard !stopped else { return false }
            if let retired = loaded.rekeyed, !retired.isEmpty { delegate?.host(self, rekeyed: retired) }
            snapshot = loaded
            snapshotSeq = seq
            reachable = true
            lastEventID = max(lastEventID, loaded.eventCursor ?? lastEventID)
            if catalogHostEpoch != loaded.hostEpoch {
                catalogHostEpoch = loaded.hostEpoch
                catalogStreamCursor = loaded.catalogSeq ?? 0
            } else {
                catalogStreamCursor = max(catalogStreamCursor, loaded.catalogSeq ?? catalogStreamCursor)
            }
            connectionError = loaded.daemon.stale == true ? (loaded.daemon.refreshError ?? "Showing saved state. The computer could not refresh its agents.") : nil
            delegate?.hostDidChange(self)
            await delegate?.hostRefreshed(self)
            return true
        } catch {
            guard !stopped else { return false }
            connectionError = error.localizedDescription
            delegate?.hostDidChange(self)
            return false
        }
    }

    private func startEvents() {
        eventTask?.cancel()
        eventTask = Task {
            let backoff = EventRetryBackoff()
            while !Task.isCancelled, !stopped {
                do {
                    try await api.streamEvents(
                        after: lastEventID,
                        onConnected: { [weak self] in await self?.eventStreamConnected() },
                        onEvent: { [weak self] event in
                            backoff.reset()
                            await self?.receive(event)
                        }
                    )
                } catch {
                    if Task.isCancelled || stopped { return }
                    reconnecting = true
                    do {
                        let version = try await api.version()
                        guard !stopped else { return }
                        build = version.build
                        recordStreamInterruption(probeError: nil)
                    } catch {
                        guard !stopped else { return }
                        recordStreamInterruption(probeError: error)
                    }
                    let delay = backoff.nextDelay()
                    try? await Task.sleep(for: .seconds(delay))
                }
            }
        }
    }

    func eventStreamConnected() async {
        guard !stopped else { return }
        reconnecting = false
        if !snapshotCoversEventStream { _ = await refresh(fresh: true) } else {
            snapshotCoversEventStream = false
            delegate?.hostDidChange(self)
        }
        guard !stopped else { return }
        await delegate?.hostEventStreamConnected(self)
        guard !stopped else { return }
        startCatalogStream()
    }

    func recordStreamInterruption(probeError: Error?) {
        reconnecting = probeError == nil
        reachable = probeError == nil
        connectionError = probeError?.localizedDescription
        delegate?.hostDidChange(self)
    }

    func receive(_ event: AgenthailEvent) async {
        guard !stopped else { return }
        lastEventID = event.type == "stream.reset" ? 0 : max(lastEventID, event.id)
        if let entity = event.entityId { delegate?.host(self, receivedEventFor: entity) }
        if event.type == "ui.changed" { delegate?.hostAppearanceChanged(self) }
        guard AgenthailIOSModel.shouldRefreshSnapshot(for: event.type) else { return }
        eventRefreshTask?.cancel()
        eventRefreshTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(200))
            guard !Task.isCancelled, let self else { return }
            await self.refresh(fresh: event.type == "stream.reset")
        }
    }

    private func startCatalogStream() {
        catalogStreamTask?.cancel()
        catalogStreamTask = Task {
            let backoff = EventRetryBackoff()
            while !Task.isCancelled, !stopped {
                do {
                    try await api.streamCatalog(after: catalogStreamCursor, onConnected: {}, onEvent: { [weak self] event in
                        backoff.reset()
                        await self?.receiveCatalog(event)
                    })
                } catch {
                    if Task.isCancelled || stopped { return }
                    if case AgenthailAPIError.streamGap = error {
                        catalogStreamCursor = 0
                        _ = await refresh(fresh: true)
                    }
                    let delay = backoff.nextDelay()
                    try? await Task.sleep(for: .seconds(delay))
                }
            }
        }
    }

    func receiveCatalog(_ event: CatalogStreamEvent) async {
        guard !stopped else { return }
        guard event.stream == "catalog" else { return }
        guard event.seq > catalogStreamCursor else { return }
        catalogStreamCursor = max(catalogStreamCursor, event.seq)
        guard var current = snapshot else { return }
        switch event.type {
        case "session.upserted":
            guard let changed = event.data.session else { _ = await refresh(); return }
            if let index = current.sessions.firstIndex(where: { $0.id == changed.id }) { current.sessions[index] = changed }
            else { current.sessions.append(changed) }
            current.totalSessions = current.sessions.count
            snapshotSeq = delegate?.nextStateRequest() ?? snapshotSeq
        case "session.queue":
            guard let id = event.data.sessionId, let count = event.data.queueCount,
                  let index = current.sessions.firstIndex(where: { $0.id == id }) else { return }
            current.sessions[index].queueCount = count
        case "session.removed":
            guard let id = event.data.sessionId else { _ = await refresh(); return }
            current.sessions.removeAll { $0.id == id }
            current.totalSessions = current.sessions.count
            snapshot = current
            delegate?.host(self, removedSession: id, unlisted: event.data.reason == "unlisted")
            if let next = event.data.rekeyedTo { delegate?.host(self, rekeyed: [id: next]) }
            delegate?.hostDidChange(self)
            return
        case "surface.health":
            guard let name = event.data.surface, let health = event.data.health,
                  let index = current.surfaces.firstIndex(where: { $0.name == name }) else {
                _ = await refresh(fresh: true)
                return
            }
            let previous = current.surfaces[index]
            current.surfaces[index] = SurfaceState(name: name, connected: health == "healthy", error: health == "healthy" ? nil : event.data.detail, health: health, healthDetail: event.data.detail, capabilities: previous.capabilities, runtime: event.data.runtime)
        case "delivery.problem":
            guard let deliveryId = event.data.deliveryId,
                  let sessionId = event.data.sessionId,
                  let message = event.data.message,
                  let reason = event.data.reason,
                  let at = event.data.at else { return }
            var problems = current.deliveryProblems ?? []
            let problem = DeliveryProblem(deliveryId: deliveryId, sessionId: sessionId, sourceSessionId: event.data.sourceSessionId, attentionSessionId: event.data.attentionSessionId, message: message, reason: reason, status: event.data.status, at: at)
            problems.removeAll { $0.deliveryId == deliveryId }
            problems.insert(problem, at: 0)
            current.deliveryProblems = Array(problems.prefix(50))
        case "delivery.dismissed":
            guard let deliveryId = event.data.deliveryId else { return }
            current.deliveryProblems = current.deliveryProblems?.filter { $0.deliveryId != deliveryId }
        default:
            return
        }
        snapshot = current
        delegate?.hostDidChange(self)
    }
}
