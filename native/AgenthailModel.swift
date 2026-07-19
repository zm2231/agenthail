import AppKit
import Foundation

@MainActor
final class AgenthailModel: ObservableObject {
    @Published var section: AppSection = .overview
    @Published var snapshot: DashboardSnapshot?
    @Published var selectedSessionID: String?
    @Published var detail: SessionDetail?
    @Published var devices: [DeviceState] = []
    @Published var pairing: PairingResponse?
    @Published var settings: DashboardSettingsState?
    @Published var audit: [HistoryState] = []
    @Published var auditKinds: [String] = []
    @Published var auditHasMore = false
    @Published var auditQuery = ""
    @Published var auditKind = ""
    @Published var connectionError: String?
    @Published var reconnecting = false
    @Published var operationError: String?
    @Published var loading = false
    @Published var composer = ""

    private var api: AgenthailAPI?
    private var connectionTask: Task<Void, Never>?
    private var eventTask: Task<Void, Never>?
    private var refreshTask: Task<Void, Never>?
    private var lastEventID: UInt64 = 0

    var isConnected: Bool { connectionError == nil && snapshot?.daemon.running == true }
    var currentSessions: [SessionState] { snapshot?.sessions.filter(\.current) ?? [] }
    var workingSessions: [SessionState] { snapshot?.sessions.filter(\.isWorking) ?? [] }
    var selectedSession: SessionState? { snapshot?.sessions.first { $0.id == selectedSessionID } }

    init() {
        connect()
    }

    deinit {
        connectionTask?.cancel()
        eventTask?.cancel()
        refreshTask?.cancel()
    }

    func connect(startDaemonIfNeeded: Bool = true) {
        eventTask?.cancel()
        connectionTask?.cancel()
        connectionTask = Task {
            let backoff = EventRetryBackoff()
            var shouldStartDaemon = startDaemonIfNeeded
            while !Task.isCancelled {
                do {
                    let api = try AgenthailAPI()
                    _ = try await api.version()
                    self.api = api
                    connectionError = nil
                    lastEventID = 0
                    guard await refresh(fresh: true) else {
                        throw AgenthailAPIError.unavailable(connectionError ?? "Agenthail could not connect.")
                    }
                    startEvents()
                    return
                } catch {
                    connectionError = error.localizedDescription
                    snapshot = nil
                    if let apiError = error as? AgenthailAPIError, case .incompatible = apiError {
                        return
                    }
                }
                if shouldStartDaemon {
                    AgenthailProcess.run(["daemon", "start"])
                    shouldStartDaemon = false
                }
                let delay = backoff.nextDelay()
                try? await Task.sleep(for: .seconds(delay))
            }
        }
    }

    @discardableResult
    func refresh(fresh: Bool = false) async -> Bool {
        guard let api else { return false }
        loading = snapshot == nil
        do {
            let loaded = try await api.snapshot(fresh: fresh)
            snapshot = loaded
            lastEventID = max(lastEventID, loaded.eventCursor ?? lastEventID)
            connectionError = nil
            if selectedSessionID == nil {
                selectedSessionID = currentSessions.first?.id
            }
            if section == .conversations, let selectedSessionID {
                await loadSession(selectedSessionID)
            }
        } catch {
            connectionError = error.localizedDescription
            loading = false
            return false
        }
        loading = false
        return true
    }

    func selectSession(_ id: String) {
        selectedSessionID = id
        detail = nil
        section = .conversations
        Task { await loadSession(id) }
    }

    func showSection(_ next: AppSection) {
        section = next
        guard next == .conversations, let selectedSessionID else { return }
        Task { await loadSession(selectedSessionID) }
    }

    func loadSession(_ id: String) async {
        guard let api else { return }
        do {
            let loaded = try await api.sessionDetail(id: id)
            guard sessionLoadIsCurrent(id, selectedID: selectedSessionID) else { return }
            detail = loaded
            operationError = nil
        } catch {
            guard sessionLoadIsCurrent(id, selectedID: selectedSessionID) else { return }
            operationError = error.localizedDescription
        }
    }

    func send() {
        let message = composer.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !message.isEmpty, let sessionID = selectedSessionID else { return }
        composer = ""
        perform(action: selectedSession?.isWorking == true ? "steer" : "send", sessionID: sessionID, message: message)
    }

    func perform(action: String, sessionID: String? = nil, message: String? = nil, model: String? = nil, queueID: Int64? = nil) {
        guard let api else { return }
        Task {
            do {
                try await api.action(action, sessionID: sessionID, message: message, model: model, queueID: queueID)
                operationError = nil
                await refresh(fresh: true)
            } catch {
                operationError = error.localizedDescription
                if let message, composer.isEmpty { composer = message }
            }
        }
    }

    func loadDevices() async {
        guard let api else { return }
        do {
            devices = try await api.devices()
        } catch {
            operationError = error.localizedDescription
        }
    }

    func loadOperations() async {
        await loadDevices()
        guard let api else { return }
        do {
            settings = try await api.settings()
            await loadAudit(reset: true)
        } catch {
            operationError = error.localizedDescription
        }
    }

    func loadAudit(reset: Bool) async {
        guard let api else { return }
        do {
            let before = reset ? 0 : (audit.last?.id ?? 0)
            let page = try await api.history(before: before, kind: auditKind, query: auditQuery)
            audit = reset ? page.items : audit + page.items
            auditKinds = page.kinds
            auditHasMore = page.hasMore
            operationError = nil
        } catch {
            operationError = error.localizedDescription
        }
    }

    func refreshCurrentSection() {
        Task {
            _ = await refresh(fresh: true)
            if section == .operations { await loadOperations() }
        }
    }

    func performOperation(action: String, message: String? = nil, channel: String? = nil, targetID: String? = nil, fromID: String? = nil, toID: String? = nil, pattern: String? = nil, relayID: Int64? = nil) {
        guard let api else { return }
        Task {
            do {
                try await api.action(action, message: message, channel: channel, targetID: targetID, fromID: fromID, toID: toID, pattern: pattern, relayID: relayID)
                operationError = nil
                _ = await refresh(fresh: true)
                await loadAudit(reset: true)
            } catch {
                operationError = error.localizedDescription
            }
        }
    }

    func updateNotifications(_ action: String) {
        guard let api else { return }
        Task {
            do {
                try await api.updateSettings(action: action)
                settings = try await api.settings()
                operationError = nil
            } catch {
                operationError = error.localizedDescription
                settings = try? await api.settings()
            }
        }
    }

    func setRemoteAccess(_ enabled: Bool) {
        guard let api else { return }
        Task {
            do {
                try await api.updateSettings(action: enabled ? "remote-enable" : "remote-disable")
                settings = try await api.settings()
                pairing = nil
                operationError = nil
            } catch {
                operationError = error.localizedDescription
            }
        }
    }

    func createPairing() {
        guard let api else { return }
        Task {
            do {
                pairing = try await api.createPairing(name: "iPhone")
                operationError = nil
            } catch {
                operationError = error.localizedDescription
            }
        }
    }

    func revokeDevice(_ id: String) {
        guard let api else { return }
        Task {
            do {
                try await api.revokeDevice(id: id)
                await loadDevices()
            } catch {
                operationError = error.localizedDescription
            }
        }
    }

    func restartDaemon() {
        AgenthailProcess.run(["daemon", "restart"])
        Task {
            try? await Task.sleep(for: .seconds(2))
            connect()
        }
    }

    private func startEvents() {
        eventTask?.cancel()
        guard let api else { return }
        eventTask = Task {
            let backoff = EventRetryBackoff()
            while !Task.isCancelled {
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
                    if Task.isCancelled { return }
                    reconnecting = true
                    do {
                        _ = try await api.version()
                        connectionError = nil
                    } catch {
                        reconnecting = false
                        connectionError = error.localizedDescription
                    }
                    let delay = backoff.nextDelay()
                    try? await Task.sleep(for: .seconds(delay))
                }
            }
        }
    }

    private func receive(_ event: AgenthailEvent) {
        if event.type == "stream.reset" {
            lastEventID = 0
        } else {
            lastEventID = max(lastEventID, event.id)
        }
        refreshTask?.cancel()
        refreshTask = Task {
            try? await Task.sleep(for: .milliseconds(180))
            if Task.isCancelled { return }
            _ = await refresh(fresh: true)
            if OperationsRefreshPolicy.reloadOperations(for: event.type) {
                await loadOperations()
            } else if section == .operations && OperationsRefreshPolicy.reloadAudit(for: event.type) {
                await loadAudit(reset: true)
            }
        }
    }

    private func eventStreamConnected() {
        reconnecting = false
        connectionError = nil
    }
}
