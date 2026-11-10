import Foundation
import os

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
    private(set) var currentView: ScopePage.View?
    private var currentViewSeq: (epoch: String?, seq: UInt64) = (nil, 0)
    private lazy var orderRereads = makeOrderRereads()
    private var refreshAtTask: Task<Void, Never>?
    private(set) var reachable = false
    private(set) var holding = false
    var shownReachable: Bool { reachable || holding }
    private(set) var reconnecting = false
    private(set) var connectionError: String?
    private(set) var pushRelayURL: URL?
    private(set) var build: AgenthailBuild?
    private(set) var catalogStreamCursor: UInt64 = 0
    private(set) var snapshotSeq: UInt64 = 0
    private var refreshOrder = RefreshOrder()
    var refreshTimeout: TimeInterval = 25
    private var snapshotCoversEventStream = false
    private var stopped = false
    private var catalogHostEpoch: String?
    private var lastEventID: UInt64 = 0
    private var connectionTask: Task<Void, Never>?
    private var eventTask: Task<Void, Never>?
    private var eventRefreshTask: Task<Void, Never>?
    var grace = ConnectionGrace()
    private var streamOpen = false
    private static let log = Logger(subsystem: "com.agenthail.app", category: "connection")
    private(set) lazy var reader = makeReader()
    var pages: ScopePages { reader.pages }
    var pageError: String? { reader.pageError }
    lazy var catalogFeed = makeCatalogFeed()
    weak var delegate: HostConnectionDelegate?
    var drops: ConnectionDrops?

    init(credential: PairedHostCredential, session: URLSession, drops: ConnectionDrops? = .shared) {
        id = credential.hostID
        self.credential = credential
        api = AgenthailAPI(baseURL: credential.endpoint, token: credential.token, session: session)
        isPreview = false
        pushRelayURL = KeychainStore.get(Self.relayAccount(credential.hostID)).flatMap(URL.init(string:))
        self.drops = drops
        api.onRelayFailure = { [weak self] host, code in
            Task { @MainActor in self?.relayFailed(to: host, code: code) }
        }
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
        refreshAtTask?.cancel()
    }

    var name: String { snapshot?.daemon.host?.name ?? credential.hostName }

    var view: PairedHostView {
        PairedHostView(id: id, name: name, reachable: reachable, holding: holding, snapshot: snapshot.map { snapshot in
            var listed = snapshot
            listed.sessions = pages.joined(snapshot.sessions)
            return listed
        })
    }

    static func relayAccount(_ hostID: String) -> String { "pushRelayURL:\(hostID)" }

    func connect() {
        guard !stopped else { return }
        connectionTask?.cancel()
        eventTask?.cancel()
        eventRefreshTask?.cancel()
        lastEventID = 0
        reconnecting = true
        streamOpen = false
        if reachable { grace.contact() }
        delegate?.hostDidChange(self)
        connectionTask = Task {
            let backoff = EventRetryBackoff()
            while !Task.isCancelled, !stopped {
                let started = Date()
                do {
                    let version = try await api.version(requiring: AgenthailAPI.snapshotProtocol, timeout: AgenthailAPI.probeTimeout)
                    guard !Task.isCancelled, !stopped else { return }
                    grace.contact()
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
                    if stopIfIncompatible(error) { return }
                    recordFailure(error, during: "connect", elapsed: Date().timeIntervalSince(started))
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
        orderRereads.cancel()
        refreshAtTask?.cancel()
        reader.cancel()
        catalogFeed.stop()
        reachable = false
        holding = false
        reconnecting = false
        grace.forget()
        drops?.stopped(host: id)
    }

    func setSnapshot(_ value: DashboardSnapshot?, view: ScopePage.View? = nil) {
        snapshot = value
        currentView = view
        reachable = value != nil
        holding = false
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
        let started = Date()
        let seq = delegate?.nextStateRequest() ?? 0
        let request = refreshOrder.begin()
        do {
            async let parts = api.snapshotBaseAndCurrent(fresh: fresh, timeout: refreshTimeout)
            async let activity = api.projectActivity(fresh: fresh, timeout: refreshTimeout)
            let (base, readCurrent) = try await parts
            let loaded = try await DashboardSnapshot(base: base, current: readCurrent, activity: activity)
            guard !stopped else { return false }
            guard refreshOrder.adopts(request) else { return true }
            if let retired = loaded.rekeyed, !retired.isEmpty { delegate?.host(self, rekeyed: retired) }
            let before = Set(snapshot?.sessions.map(\.id) ?? [])
            snapshot = loaded.keepingCatalog(of: snapshot, appliedThrough: catalogStreamCursor)
            reader.refreshed(removedFromCurrent: !before.subtracting(snapshot?.sessions.map(\.id) ?? []).isEmpty)
            snapshotSeq = seq
            _ = drops?.peersConnected(via: id, Set((loaded.daemon.hosts ?? []).filter { $0.state == "connected" }.map(\.id)))
            let settles = refreshOrder.settles(request)
            if settles { recovered(by: "snapshot") }
            lastEventID = max(lastEventID, loaded.eventCursor ?? lastEventID)
            if catalogHostEpoch != loaded.hostEpoch {
                if catalogHostEpoch != nil { reader.rereadAll() }
                catalogHostEpoch = loaded.hostEpoch
                catalogStreamCursor = loaded.catalogSeq ?? 0
                if catalogFeed.isRunning { catalogFeed.start() }
            } else {
                catalogStreamCursor = max(catalogStreamCursor, loaded.catalogSeq ?? catalogStreamCursor)
            }
            adoptCurrentView(readCurrent)
            if settles { connectionError = loaded.daemon.stale == true ? (loaded.daemon.refreshError ?? "Showing saved state. The computer could not refresh its agents.") : nil }
            delegate?.hostDidChange(self)
            await delegate?.hostRefreshed(self)
            return true
        } catch {
            guard !stopped else { return false }
            if ConnectionGrace.isTransport(error) {
                drops?.failed(host: id, path: "request", error: ConnectionDropLog.code(error), elapsed: Date().timeIntervalSince(started), shown: shownReachable ? .held : .offline)
            }
            guard !error.isCancellation, !Task.isCancelled, refreshOrder.settles(request) else { return false }
            connectionError = error.localizedDescription
            delegate?.hostDidChange(self)
            return false
        }
    }

    func readMore(_ request: ScopeRequest) async { await reader.readMore(request) }

    func view(_ request: ScopeRequest) -> ScopePage.View? { reader.views[request] }

    func search(_ query: String, in request: ScopeRequest) async { await reader.search(query, in: request) }

    func mentions(_ query: String) async throws -> [SessionState] { try await reader.mentions(query) }

    private func makeReader() -> ScopeReader {
        ScopeReader(ScopeReader.Source(
            api: { [weak self] in self?.api },
            current: { [weak self] in self?.snapshot?.sessions ?? [] },
            active: { [weak self] in self.map { !$0.stopped } ?? false },
            reachable: { [weak self] in self?.reachable ?? false },
            changed: { [weak self] in
                guard let self else { return }
                delegate?.hostDidChange(self)
            },
            timeout: { [weak self] in self?.refreshTimeout ?? 25 }
        ))
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
                    if streamOpen { grace.contact() }
                    streamOpen = false
                    Self.log.notice("\(self.id, privacy: .public) event stream ended: \(error.localizedDescription, privacy: .public)")
                    drops?.failed(host: id, path: "stream", error: ConnectionDropLog.code(error), elapsed: nil, shown: shownReachable ? .held : .offline)
                    reconnecting = true
                    let probed = Date()
                    do {
                        let version = try await api.version(requiring: AgenthailAPI.snapshotProtocol, timeout: AgenthailAPI.probeTimeout)
                        guard !stopped else { return }
                        build = version.build
                        recordStreamInterruption(probeError: nil)
                    } catch {
                        guard !stopped else { return }
                        if stopIfIncompatible(error) { return }
                        recordStreamInterruption(probeError: error, elapsed: Date().timeIntervalSince(probed))
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
        streamOpen = true
        recovered(by: "event stream")
        if !snapshotCoversEventStream { _ = await refresh(fresh: true) } else {
            snapshotCoversEventStream = false
            delegate?.hostDidChange(self)
        }
        guard !stopped else { return }
        await delegate?.hostEventStreamConnected(self)
        guard !stopped else { return }
        catalogFeed.start()
    }

    private func stopIfIncompatible(_ error: Error) -> Bool {
        guard let apiError = error as? AgenthailAPIError, case .incompatible(let hostProtocol) = apiError else { return false }
        reachable = false
        holding = false
        reconnecting = false
        connectionError = hostProtocol < AgenthailAPI.snapshotProtocol ? "Update Agenthail on \(name) to connect." : apiError.localizedDescription
        delegate?.hostDidChange(self)
        return true
    }

    func recordStreamInterruption(probeError: Error?, elapsed: TimeInterval? = nil) {
        guard let probeError else {
            recovered(by: "stream probe")
            reconnecting = true
            connectionError = nil
            delegate?.hostDidChange(self)
            return
        }
        recordFailure(probeError, during: "stream probe", elapsed: elapsed)
    }

    private func recovered(by step: String) {
        if !reachable || holding { Self.log.notice("\(self.id, privacy: .public) reachable again after \(step, privacy: .public)") }
        grace.contact()
        reachable = true
        holding = false
        _ = drops?.recovered(host: id, by: step)
        uploadDrops()
    }

    private func uploadDrops() {
        guard let drops else { return }
        let id = id, api = api
        Task { await drops.upload(to: id, with: api) }
    }

    private func relayFailed(to host: String, code: String) {
        guard !stopped, host != id else { return }
        drops?.failed(host: host, via: id, path: "request", error: "relay.\(code)", elapsed: nil, shown: .offline)
    }

    private func recordFailure(_ error: Error, during step: String, elapsed: TimeInterval? = nil) {
        let held = (reachable || holding) && grace.holds(error)
        drops?.failed(host: id, path: step, error: ConnectionDropLog.code(error), elapsed: elapsed, shown: held ? .held : .offline)
        if held {
            Self.log.notice("\(self.id, privacy: .public) \(step, privacy: .public) failed, still shown connected: \(error.localizedDescription, privacy: .public)")
        } else if reachable || holding {
            Self.log.notice("\(self.id, privacy: .public) shown unreachable after \(step, privacy: .public): \(error.localizedDescription, privacy: .public)")
        }
        reachable = false
        holding = held
        reconnecting = held
        connectionError = held ? nil : error.localizedDescription
        delegate?.hostDidChange(self)
    }

    func receive(_ event: AgenthailEvent) async {
        guard !stopped else { return }
        lastEventID = event.type == "stream.reset" ? 0 : max(lastEventID, event.id)
        if let entity = event.entityId { delegate?.host(self, receivedEventFor: entity) }
        if event.type == "ui.changed" { delegate?.hostAppearanceChanged(self) }
        guard SnapshotRefreshPolicy.refreshes(for: event.type) else { return }
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
        let fresh = events.filter { $0.stream == "catalog" && $0.seq > catalogStreamCursor }
        let held = reader.joined(current.sessions)
        let news = CatalogNews.moves(fresh, held: Dictionary(held.map { ($0.id, $0) }, uniquingKeysWith: { first, _ in first }))
        let batch = current.applyingCatalog(events, after: catalogStreamCursor)
        catalogStreamCursor = batch.cursor
        if batch.changed {
            if batch.sessionsChanged { snapshotSeq = delegate?.nextStateRequest() ?? snapshotSeq }
            snapshot = batch.snapshot
            reader.adoptCurrent()
            for removal in batch.removals {
                _ = reader.removed(removal.id, unlisted: removal.unlisted)
                delegate?.host(self, removedSession: removal.id, unlisted: removal.unlisted)
                if let next = removal.rekeyedTo { delegate?.host(self, rekeyed: [removal.id: next]) }
            }
            delegate?.hostDidChange(self)
        }
        if news { rereadOrder() }
        return batch.needsSnapshot
    }

    private func adoptCurrentView(_ read: ScopeSessions) {
        guard let view = read.view else { return }
        let seq = read.catalogSeq ?? 0
        if let epoch = catalogHostEpoch, read.hostEpoch != epoch { return }
        if read.hostEpoch == currentViewSeq.epoch, seq < currentViewSeq.seq { return }
        currentViewSeq = (read.hostEpoch, seq)
        currentView = view
        refreshAtTask?.cancel()
        guard let at = view.refreshAt.flatMap(TimestampParser.date) else { return }
        refreshAtTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(max(1, at.timeIntervalSinceNow)))
            guard !Task.isCancelled else { return }
            self?.rereadOrder()
        }
    }

    func rereadOrder() { orderRereads.request() }

    private func makeOrderRereads() -> Coalescer {
        Coalescer(delay: .milliseconds(300)) { [weak self] in await self?.readOrder() }
    }

    private func readOrder() async {
        guard !stopped else { return }
        reader.rereadAll()
        if let read = try? await api.scopeSessions("current", timeout: refreshTimeout) {
            adoptCurrentView(read)
            if var merged = snapshot {
                let held = Set(merged.sessions.map(\.id))
                merged.sessions += read.sessions.filter { !held.contains($0.id) }
                snapshot = merged
            }
            delegate?.hostDidChange(self)
        }
    }
}
