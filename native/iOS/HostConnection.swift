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
    private(set) var reachable = false
    private(set) var holding = false
    var shownReachable: Bool { reachable || holding }
    private(set) var reconnecting = false
    private(set) var connectionError: String?
    private(set) var pushRelayURL: URL?
    private(set) var build: AgenthailBuild?
    private(set) var catalogStreamCursor: UInt64 = 0
    private(set) var snapshotSeq: UInt64 = 0
    private var refreshGeneration: UInt64 = 0
    private var appliedGeneration: UInt64 = 0
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
    private(set) var pages = ScopePages()
    private(set) var pageError: String?
    private var readingScopes: Set<String> = []
    private var searchGeneration: UInt64 = 0
    private var rereadTask: Task<Void, Never>?
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
        rereadTask?.cancel()
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
                do {
                    let version = try await api.version(requiring: AgenthailAPI.snapshotProtocol)
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
                    recordFailure(error, during: "connect")
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
        rereadTask?.cancel()
        catalogFeed.stop()
        reachable = false
        holding = false
        reconnecting = false
        grace.forget()
    }

    func setSnapshot(_ value: DashboardSnapshot?) {
        snapshot = value
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
        let seq = delegate?.nextStateRequest() ?? 0
        refreshGeneration += 1
        let generation = refreshGeneration
        do {
            async let base = api.snapshotBase(fresh: fresh, timeout: refreshTimeout)
            async let current = api.scopeSessions("current", timeout: refreshTimeout)
            async let activity = api.projectActivity(fresh: fresh, timeout: refreshTimeout)
            let loaded = try await DashboardSnapshot(base: base, current: current, activity: activity)
            guard !stopped else { return false }
            guard generation > appliedGeneration else { return true }
            appliedGeneration = generation
            if let retired = loaded.rekeyed, !retired.isEmpty { delegate?.host(self, rekeyed: retired) }
            snapshot = loaded.keepingCatalog(of: snapshot, appliedThrough: catalogStreamCursor)
            pages.adopt(snapshot?.sessions ?? [])
            snapshotSeq = seq
            recovered(by: "snapshot")
            lastEventID = max(lastEventID, loaded.eventCursor ?? lastEventID)
            if catalogHostEpoch != loaded.hostEpoch {
                if catalogHostEpoch != nil { rereadPages() }
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

    func readMore(_ scope: String) async {
        guard !stopped, reachable, !readingScopes.contains(scope), let read = pages.next(scope) else { return }
        readingScopes.insert(scope)
        defer { readingScopes.remove(scope) }
        do {
            let page = try await api.scopePage(scope, cursor: read.cursor, timeout: refreshTimeout)
            guard !stopped else { return }
            pageError = nil
            if pages.append(page, scope: scope, after: read) { pages.adopt(snapshot?.sessions ?? []) }
        } catch {
            guard !stopped, !Task.isCancelled else { return }
            pageError = error.localizedDescription
        }
        delegate?.hostDidChange(self)
    }

    func search(_ query: String, scope: String) async {
        searchGeneration += 1
        let generation = searchGeneration
        guard !query.isEmpty, reachable else {
            if !pages.found.isEmpty {
                pages.setFound([])
                delegate?.hostDidChange(self)
            }
            return
        }
        let page = try? await api.scopePage(scope, query: query, timeout: refreshTimeout)
        guard !stopped, !Task.isCancelled, generation == searchGeneration else { return }
        pages.setFound(page?.sessions ?? [])
        delegate?.hostDidChange(self)
    }

    func mentions(_ query: String) async throws -> [SessionState] {
        try await api.scopePage("all", query: query, limit: 20, timeout: refreshTimeout).sessions
    }

    private func rereadPages() {
        rereadTask?.cancel()
        rereadTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(300))
            guard let self, !Task.isCancelled else { return }
            for scope in pages.listings.keys.sorted() { await reread(scope) }
        }
    }

    private func reread(_ scope: String) async {
        guard !stopped, let start = pages.restart(scope) else { return }
        var rows: [SessionState] = []
        var cursor: String?
        do {
            repeat {
                let page = try await api.scopePage(scope, cursor: cursor, limit: 200, timeout: refreshTimeout)
                rows += page.sessions
                cursor = page.nextCursor
            } while cursor != nil && rows.count < start.rows
        } catch {
            return
        }
        guard !stopped, !Task.isCancelled, pages.replace(scope, rows: rows, cursor: cursor, generation: start.generation) else { return }
        pages.adopt(snapshot?.sessions ?? [])
        delegate?.hostDidChange(self)
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
                    reconnecting = true
                    do {
                        let version = try await api.version(requiring: AgenthailAPI.snapshotProtocol)
                        guard !stopped else { return }
                        build = version.build
                        recordStreamInterruption(probeError: nil)
                    } catch {
                        guard !stopped else { return }
                        if stopIfIncompatible(error) { return }
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

    func recordStreamInterruption(probeError: Error?) {
        guard let probeError else {
            recovered(by: "stream probe")
            reconnecting = true
            connectionError = nil
            delegate?.hostDidChange(self)
            return
        }
        recordFailure(probeError, during: "stream probe")
    }

    private func recovered(by step: String) {
        if !reachable || holding { Self.log.notice("\(self.id, privacy: .public) reachable again after \(step, privacy: .public)") }
        grace.contact()
        reachable = true
        holding = false
    }

    private func recordFailure(_ error: Error, during step: String) {
        let held = (reachable || holding) && grace.holds(error)
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
            pages.adopt(batch.snapshot.sessions)
            var reread = false
            for removal in batch.removals {
                if pages.remove(removal.id) { reread = true }
                delegate?.host(self, removedSession: removal.id, unlisted: removal.unlisted)
                if let next = removal.rekeyedTo { delegate?.host(self, rekeyed: [removal.id: next]) }
            }
            if reread { rereadPages() }
            delegate?.hostDidChange(self)
        }
        return batch.needsSnapshot
    }
}
