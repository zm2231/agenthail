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
    lazy var catalogFeed = makeCatalogFeed()
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
        catalogFeed.stop()
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
            snapshot = loaded.keepingCatalog(of: snapshot, appliedThrough: catalogStreamCursor)
            snapshotSeq = seq
            reachable = true
            lastEventID = max(lastEventID, loaded.eventCursor ?? lastEventID)
            if catalogHostEpoch != loaded.hostEpoch {
                catalogHostEpoch = loaded.hostEpoch
                catalogStreamCursor = loaded.catalogSeq ?? 0
                if catalogFeed.isRunning { catalogFeed.start() }
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
        catalogFeed.start()
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

    private func makeCatalogFeed() -> CatalogFeed {
        CatalogFeed(CatalogFeed.Host(
            api: { [weak self] in self?.api },
            cursor: { [weak self] in self?.catalogStreamCursor ?? 0 },
            refresh: { [weak self] in await self?.refresh(fresh: true) ?? false },
            resetCursor: { [weak self] in self?.catalogStreamCursor = 0 },
            apply: { [weak self] events in
                guard let self, applyCatalog(events) else { return }
                Task { await self.refresh(fresh: true) }
            },
            stopped: { [weak self] in self?.stopped ?? true }
        ))
    }

    func receiveCatalog(_ events: [CatalogStreamEvent]) async {
        if applyCatalog(events) { _ = await refresh(fresh: true) }
    }

    private func applyCatalog(_ events: [CatalogStreamEvent]) -> Bool {
        guard !stopped, let current = snapshot else { return false }
        let batch = current.applyingCatalog(events, after: catalogStreamCursor)
        catalogStreamCursor = batch.cursor
        if batch.changed {
            if batch.sessionsChanged { snapshotSeq = delegate?.nextStateRequest() ?? snapshotSeq }
            snapshot = batch.snapshot
            for removal in batch.removals {
                delegate?.host(self, removedSession: removal.id, unlisted: removal.unlisted)
                if let next = removal.rekeyedTo { delegate?.host(self, rekeyed: [removal.id: next]) }
            }
            delegate?.hostDidChange(self)
        }
        return batch.needsSnapshot
    }
}
