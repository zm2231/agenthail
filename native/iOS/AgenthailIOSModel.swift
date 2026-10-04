import Foundation
import UIKit
import UserNotifications

@MainActor
final class AgenthailIOSModel: ObservableObject {
    static func shouldRefreshSnapshot(for eventType: String) -> Bool {
        eventType == "stream.reset" || eventType == "state.changed" || eventType == "settings.updated" || eventType.hasPrefix("device.")
    }

    @Published var snapshot: DashboardSnapshot?
    @Published var selectedDetail: SessionDetail?
    @Published var selectedSessionID: String?
    @Published var connectionError: String?
    @Published var reconnecting = false
    @Published var operationError: String?
    @Published var pairing = false
    @Published var composer = ""
    @Published var sessionError: String?
    @Published var loadingSession = false
    @Published var sendingSessionIDs: Set<String> = []
    @Published var deliveryStatus: [String: String] = [:]
    private var deliveryQueueIDs: [String: Int64] = [:]
    private var refreshingDeliveries = false
    @Published var olderActivity: [TimelineItem] = []
    @Published var activityCursor: Int64?
    @Published var loadingOlderActivity = false
    @Published var olderActivityError: String?
    @Published var searchResults: [SessionSearchItem] = []
    @Published var searchError: String?
    @Published var searching = false
    private var searchQuery = ""
    private var sessionRequestID = UUID()
    private var drafts: [String: String] = [:]
    @Published var notificationStatus = "Not enabled"
    @Published var requestedSessionID: String?
    @Published var showForgetMacConfirmation = false
    @Published var showPairingConfirmation = false

    private var api: AgenthailAPI?
    @Published var creatingSession = false
    @Published var creationError: String?
    @Published var pendingControls: Set<String> = []
    @Published private(set) var turnSettingsDrafts: [String: TurnSettings] = [:]

    private func deliveryLabel(_ evidence: String?, detail: String?) -> String {
        switch evidence {
        case "submitted": return detail ?? "Submitted"
        case "queued": return detail ?? "Queued"
        case "transport_accepted", "delivered": return detail ?? "Sent"
        case "failed": return "Delivery failed"
        case "unknown", nil: return "Delivery outcome unknown. Check activity before retrying."
        case "held": return "Delivery held. Review it in Inbox before retrying."
        case "expired": return "Instruction expired. Review it in Inbox history."
        case "canceled": return "Instruction canceled."
        default: return "Delivery status: \(evidence ?? "unknown")"
        }
    }

    func turnSettings(for sessionID: String) -> TurnSettings {
        turnSettingsDrafts[sessionID] ?? TurnSettings()
    }

    func setTurnSettings(_ settings: TurnSettings, for sessionID: String) {
        if settings.isEmpty { turnSettingsDrafts.removeValue(forKey: sessionID) }
        else { turnSettingsDrafts[sessionID] = settings }
    }

    func clearTurnSettings(for sessionID: String) {
        turnSettingsDrafts.removeValue(forKey: sessionID)
    }

    func queuedInstructions() async throws -> [QueueState] {
        guard let api else { throw AgenthailAPIError.unavailable("Connect to your Mac first.") }
        return try await api.queuedInstructions()
    }

    func editSession(id: String, action: String, text: String = "") async throws {
        guard let api else { throw AgenthailAPIError.unavailable("Connect to your Mac first.") }
        guard !pendingControls.contains(id) else { throw AgenthailAPIError.unavailable("An update is already in progress.") }
        if action != "alias" {
            guard let detail = selectedDetail, detail.session.id == id, !detail.readOnly, detail.capabilities.goal,
                  ["goal-set", "goal-edit", "goal-clear", "goal-pause", "goal-resume", "goal-budget"].contains(action) else { throw AgenthailAPIError.unavailable("This session cannot change goals.") }
        }
        pendingControls.insert(id)
        defer { pendingControls.remove(id) }
        if action == "alias" { try await api.nameSession(id: id, alias: text) }
        else { try await api.action(action, sessionID: id, message: text) }
        await refresh(fresh: true)
        await refreshSession(id)
    }

    func updateQueue(_ item: QueueState, retry: Bool) async throws {
        guard let api else { throw AgenthailAPIError.unavailable("Connect to your Mac first.") }
        let key = "queue:\(item.id)"
        guard !pendingControls.contains(key) else { return }
        pendingControls.insert(key)
        defer { pendingControls.remove(key) }
        try await api.action(retry ? "queue-retry" : "queue-cancel", queueID: item.id)
        await refresh(fresh: true)
    }

    func sessionOptions() async throws -> SessionCreationOptions {
        guard let api else { throw AgenthailAPIError.unavailable("Connect to your Mac first.") }
        return try await api.sessionOptions()
    }
    func creationModels(surface: String) async throws -> [ModelOption] {
        guard let api else { throw AgenthailAPIError.unavailable("Connect to your Mac first.") }
        return try await api.creationModels(surface: surface)
    }
    func createSession(surface: String, message: String, cwd: String, model: String, turnSettings: TurnSettings = .init(), claude: ClaudeCreationSettings = .init()) async -> Bool {
        guard !creatingSession, let api, !message.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return false }
        creatingSession = true; creationError = nil
        defer { creatingSession = false }
        do {
            let receipt = try await api.createSession(surface: surface, message: message, cwd: cwd, model: model, turnSettings: surface == "codex" ? turnSettings : .init(), claude: claude)
            if receipt.unknown == true && receipt.id == nil {
                creationError = "\(surface.capitalized) may have started without a confirmed session ID. Check the agent catalog on your Mac before retrying. \(receipt.error ?? "")"
                return false
            }
            guard let id = receipt.id, !id.isEmpty, receipt.ok || receipt.unknown == true else { throw AgenthailAPIError.invalidResponse }
            deliveryStatus[id] = receipt.unknown == true ? "First instruction unconfirmed. Check activity before retrying." : (surface == "claude" ? "Background session registered. Waiting for activity." : "Conversation started")
            requestedSessionID = id
            return true
        } catch {
            creationError = "Creation unconfirmed. Check All sessions before retrying to avoid starting twice. \(error.localizedDescription)"
            return false
        }
    }
    private var endpoint: URL?
    private var token: String?
    private var eventTask: Task<Void, Never>?
    private var eventRefreshTask: Task<Void, Never>?
    private var catalogStreamTask: Task<Void, Never>?
    private(set) var catalogStreamCursor: UInt64 = 0
    private var catalogHostEpoch: String?
    private var sessionStreamTask: Task<Void, Never>?
    private var sessionMetadataTask: Task<Void, Never>?
    private(set) var sessionStreamCursor: UInt64 = 0
    private var sessionStreamMetadataFields: Set<String> = []
    private var connectionTask: Task<Void, Never>?
    private var lastEventID: UInt64 = 0
    private var pushRelayURL: URL?
    private var pendingPairing: PairingLink?
    private let networkSession: URLSession
    private let automaticallyConnect: Bool

    var isPaired: Bool { endpoint != nil && token != nil }
    var currentSessions: [SessionState] { snapshot?.sessions.filter(\.current) ?? [] }

    init(autoConnect: Bool = true, session: URLSession = .shared) {
        networkSession = session
        automaticallyConnect = autoConnect
        if let endpointValue = KeychainStore.get("endpoint"), let endpoint = URL(string: endpointValue), let token = KeychainStore.get("token") {
            self.endpoint = endpoint
            self.token = token
            if autoConnect { connect() }
        }
    }

    init(api: AgenthailAPI, paired: Bool = false) {
        networkSession = .shared
        automaticallyConnect = false
        self.api = api
        if paired {
            endpoint = URL(string: "https://preview.invalid")
            token = "public-demo"
        }
    }

    deinit {
        connectionTask?.cancel()
        eventTask?.cancel()
        eventRefreshTask?.cancel()
        catalogStreamTask?.cancel()
        sessionStreamTask?.cancel()
    }

    func handlePairingURL(_ url: URL) {
        do {
            pendingPairing = try PairingLink(url: url)
            operationError = nil
            showPairingConfirmation = true
        } catch {
            pendingPairing = nil
            operationError = error.localizedDescription
        }
    }

    var pairingConfirmationTitle: String {
        isPaired ? "Replace connected Mac?" : "Connect to this Mac?"
    }

    var pairingConfirmationMessage: String {
        guard let host = pendingPairing?.endpoint.host else { return "" }
        return isPaired ? "This will replace the saved Mac with \(host)." : "Agenthail will connect securely to \(host) over Tailscale."
    }

    func confirmPairing() {
        guard let pendingPairing else { return }
        self.pendingPairing = nil
        showPairingConfirmation = false
        pairing = true
        Task {
            var newAPI: AgenthailAPI?
            do {
                let previousEndpoint = endpoint
                let previousToken = token
                let previousRelayURL = pushRelayURL ?? KeychainStore.get("pushRelayURL").flatMap(URL.init(string:))
                let previousRegistration = storedPushRegistration()
                let name = UIDevice.current.name
                let response = try await AgenthailAPI.completePairing(endpoint: pendingPairing.endpoint, secret: pendingPairing.secret, name: name, session: networkSession)
                let pairedAPI = AgenthailAPI(baseURL: pendingPairing.endpoint, token: response.token, session: networkSession)
                newAPI = pairedAPI
                if let previousRegistration, let previousRelayURL {
                    do {
                        try await PushRelayClient(baseURL: previousRelayURL, session: networkSession).revoke(previousRegistration)
                    } catch {
                        try? await pairedAPI.revokeCurrentDevice()
                        throw AgenthailAPIError.unavailable("The previous notification connection could not be revoked. Try replacing the Mac again.")
                    }
                }
                if let previousEndpoint, let previousToken {
                    let previousAPI = AgenthailAPI(baseURL: previousEndpoint, token: previousToken, session: networkSession)
                    try? await previousAPI.removePush()
                    try? await previousAPI.revokeCurrentDevice()
                }
                clearStoredPushRegistration()
                try KeychainStore.set(pendingPairing.endpoint.absoluteString, account: "endpoint")
                try KeychainStore.set(response.token, account: "token")
                self.endpoint = pendingPairing.endpoint
                token = response.token
                api = pairedAPI
                pairing = false
                operationError = nil
                if automaticallyConnect {
                    connect()
                    requestNotifications()
                }
            } catch {
                if let newAPI { try? await newAPI.revokeCurrentDevice() }
                pairing = false
                operationError = error.localizedDescription
            }
        }
    }

    func cancelPairing() {
        pendingPairing = nil
        showPairingConfirmation = false
    }

    func connect() {
        guard let endpoint, let token else { return }
        let api = AgenthailAPI(baseURL: endpoint, token: token, session: networkSession)
        self.api = api
        connectionTask?.cancel()
        eventTask?.cancel()
        eventRefreshTask?.cancel()
        lastEventID = 0
        reconnecting = true
        connectionTask = Task {
            let backoff = EventRetryBackoff()
            while !Task.isCancelled {
                do {
                    let version = try await api.version()
                    guard !Task.isCancelled else { return }
                    if let value = version.pushRelayUrl, let url = URL(string: value) {
                        pushRelayURL = url
                        try? KeychainStore.set(value, account: "pushRelayURL")
                    } else if let value = KeychainStore.get("pushRelayURL") {
                        pushRelayURL = URL(string: value)
                    }
                    if await refresh(fresh: true) {
                        guard !Task.isCancelled else { return }
                        startEvents()
                        refreshNotificationRegistration()
                        return
                    }
                } catch {
                    connectionError = error.localizedDescription
                    if let apiError = error as? AgenthailAPIError, case .incompatible = apiError {
                        reconnecting = false
                        return
                    }
                }
                reconnecting = true
                let delay = backoff.nextDelay()
                try? await Task.sleep(for: .seconds(delay))
            }
        }
    }

    func resumeConnection() {
        refreshNotificationRegistration()
        guard isPaired else { return }
        connect()
    }

    @discardableResult
    func refresh(fresh: Bool = false) async -> Bool {
        guard let api else { return false }
        do {
            let loaded = try await api.snapshot(fresh: fresh)
            snapshot = loaded
            lastEventID = max(lastEventID, loaded.eventCursor ?? lastEventID)
            if catalogHostEpoch != loaded.hostEpoch {
                catalogHostEpoch = loaded.hostEpoch
                catalogStreamCursor = loaded.catalogSeq ?? 0
            } else {
                catalogStreamCursor = max(catalogStreamCursor, loaded.catalogSeq ?? catalogStreamCursor)
            }
            connectionError = loaded.daemon.stale == true ? (loaded.daemon.refreshError ?? "Showing saved state. The Mac could not refresh its agents.") : nil
            await refreshDeliveries()
            return true
        } catch {
            connectionError = error.localizedDescription
            return false
        }
    }

    func loadSession(_ id: String) async {
        if let previous = selectedSessionID, previous != id { drafts[previous] = composer }
        if selectedSessionID != id {
            sessionStreamTask?.cancel()
            sessionMetadataTask?.cancel()
            sessionStreamCursor = 0
            sessionStreamMetadataFields.removeAll()
            composer = drafts[id] ?? ""
            selectedDetail = nil
            olderActivity = []
            activityCursor = nil
            olderActivityError = nil
        }
        selectedSessionID = id
        sessionError = nil
        loadingSession = selectedDetail == nil
        await refreshSession(id)
        if selectedSessionID == id {
            startSessionStream(id)
            startSessionMetadata(id, requestID: sessionRequestID)
        }
        if selectedSessionID == id { loadingSession = false }
    }

    func refreshSession(_ id: String) async {
        guard let api, sessionLoadIsCurrent(id, selectedID: selectedSessionID) else { return }
        let requestID = UUID()
        sessionRequestID = requestID
        do {
            let detail = try await api.sessionDetail(id: id, includeTimeline: true)
            guard sessionLoadIsCurrent(id, selectedID: selectedSessionID), sessionRequestID == requestID else { return }
            if !olderActivity.isEmpty {
                let retained = Set(olderActivity.map(\.id) + (detail.timeline?.items.map(\.id) ?? []))
                olderActivity += (selectedDetail?.timeline?.items ?? []).filter { !retained.contains($0.id) }
            }
            selectedDetail = detail
            if let journalSeq = detail.journalSeq {
                sessionStreamCursor = journalSeq
            }
            if olderActivity.isEmpty { activityCursor = detail.timeline?.nextBefore }
            sessionError = nil
        } catch {
            guard sessionLoadIsCurrent(id, selectedID: selectedSessionID), sessionRequestID == requestID else { return }
            sessionError = error.localizedDescription
        }
        await refreshDeliveries()
    }

    private func refreshDeliveries() async {
        guard let api, !deliveryQueueIDs.isEmpty, !refreshingDeliveries else { return }
        refreshingDeliveries = true
        defer { refreshingDeliveries = false }
        let expected = deliveryQueueIDs
        do {
            let items = try await api.queuedInstructions()
            for (sessionID, queueID) in expected where deliveryQueueIDs[sessionID] == queueID {
                guard let item = items.first(where: { $0.id == queueID && $0.sessionId == sessionID }) else {
                    deliveryStatus[sessionID] = "Delivery is no longer in recent history. Check the session before retrying."
                    continue
                }
                switch item.isHistorical && item.evidence == "unknown" ? "unknown-expired" : item.isHistorical && item.evidence == "failed" ? "failed-expired" : item.evidence {
                case "submitted": deliveryStatus[sessionID] = "Submitted to \(item.target)."
                case "queued": deliveryStatus[sessionID] = "Queued for \(item.target); sends when current turn ends."
                case "unknown": deliveryStatus[sessionID] = "Delivery outcome unknown. Review it in Inbox before sending again."
                case "failed": deliveryStatus[sessionID] = "Delivery failed. Review it in Inbox before retrying."
                case "unknown-expired": deliveryStatus[sessionID] = "Delivery outcome was never confirmed and later expired. Review it in Inbox history before sending again."
                case "failed-expired": deliveryStatus[sessionID] = "Delivery failed; the queue entry has expired. Review it in Inbox history."
                case "expired": deliveryStatus[sessionID] = "Instruction expired. You can review it in Inbox history."
                case "transport_accepted", "delivered": deliveryStatus[sessionID] = "Sent to \(item.target)."
                case "held": deliveryStatus[sessionID] = "Delivery held. Review it in Inbox before retrying."
                case "canceled": deliveryStatus[sessionID] = "Instruction canceled."
                default: deliveryStatus[sessionID] = "Delivery status unavailable. Check Inbox before retrying."
                }
                if item.isHistorical { deliveryQueueIDs.removeValue(forKey: sessionID) }
            }
        } catch {
            for (sessionID, queueID) in expected where deliveryQueueIDs[sessionID] == queueID {
                deliveryStatus[sessionID] = "Delivery status could not be refreshed. Check Inbox before retrying."
            }
        }
    }

    private func startSessionMetadata(_ id: String, requestID: UUID) {
        sessionMetadataTask?.cancel()
        guard let api, sessionLoadIsCurrent(id, selectedID: selectedSessionID) else { return }
        sessionMetadataTask = Task { [weak self, api] in
            do {
                let metadata = try await api.sessionMetadata(id: id)
                guard !Task.isCancelled else { return }
                self?.applySessionMetadata(metadata, for: id, requestID: requestID)
            } catch {
                return
            }
        }
    }

    private func applySessionMetadata(_ metadata: SessionMetadata, for id: String, requestID: UUID) {
        guard sessionLoadIsCurrent(id, selectedID: selectedSessionID), sessionRequestID == requestID,
              var detail = selectedDetail, detail.session.id == id else { return }
        if !sessionStreamMetadataFields.contains("context"), let context = metadata.context { detail.context = context }
        if !sessionStreamMetadataFields.contains("goal"), let goal = metadata.goal { detail.goal = goal }
        if let model = metadata.model { detail.model = model }
        if let models = metadata.models { detail.models = models }
        selectedDetail = detail
    }

    func loadOlderActivity() async {
        guard let api, let id = selectedSessionID, let cursor = activityCursor, cursor > 0, !loadingOlderActivity else { return }
        loadingOlderActivity = true
        defer { loadingOlderActivity = false }
        do {
            let page = try await api.sessionDetail(id: id, includeTimeline: true, timelineBefore: cursor)
            guard selectedSessionID == id else { return }
            guard let timeline = page.timeline, timeline.unavailableReason == nil else {
                throw AgenthailAPIError.unavailable(page.timeline?.unavailableReason ?? "Older activity is unavailable.")
            }
            let known = Set(olderActivity.map(\.id) + (selectedDetail?.timeline?.items.map(\.id) ?? []))
            olderActivity = timeline.items.filter { !known.contains($0.id) } + olderActivity
            activityCursor = timeline.nextBefore
            olderActivityError = nil
        } catch {
            if selectedSessionID == id { olderActivityError = error.localizedDescription }
        }
    }

    func searchSessions(_ query: String) async {
        searchQuery = query
        searchResults = []
        searchError = nil
        searching = false
        guard query.trimmingCharacters(in: .whitespacesAndNewlines).count >= 3, let api else { return }
        searching = true
        do {
            try await Task.sleep(for: .milliseconds(350))
            let response = try await api.searchSessions(query: query)
            guard !Task.isCancelled, searchQuery == query else { return }
            searchResults = response.results
            searchError = response.remoteError?.isEmpty == false ? response.remoteError : nil
        } catch {
            guard !Task.isCancelled, searchQuery == query else { return }
            searchError = error.localizedDescription
        }
        if searchQuery == query { searching = false }
    }

    func openNotification(_ sessionID: String) {
        requestedSessionID = sessionID
    }

    func send(to session: SessionState) {
        let message = composer.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !message.isEmpty, let api,
              let detail = selectedDetail, detail.session.id == session.id,
              !detail.readOnly, !sendingSessionIDs.contains(session.id) else { return }
        let action = "send"
        guard detail.capabilities.send else { return }
        let turnSettings = action == "send" && session.surface == "codex" ? turnSettingsDrafts[session.id] ?? TurnSettings() : TurnSettings()
        sendingSessionIDs.insert(session.id)
        deliveryQueueIDs.removeValue(forKey: session.id)
        deliveryStatus[session.id] = "Sending…"
        composer = ""
        drafts[session.id] = ""
        Task {
            defer { sendingSessionIDs.remove(session.id) }
            do {
                let response = try await api.sendInstruction(action: action, sessionID: session.id, message: message, turnSettings: turnSettings)
                deliveryStatus[session.id] = deliveryLabel(response.result?.evidence, detail: response.result?.detail)
                if response.result?.evidence == "queued", let queueID = response.result?.queueId {
                    deliveryQueueIDs[session.id] = queueID
                }
                await refreshSession(session.id)
                if action == "send", turnSettingsDrafts[session.id] == turnSettings {
                    clearTurnSettings(for: session.id)
                }
            } catch {
                deliveryStatus[session.id] = "Delivery unconfirmed. Draft kept; check activity before retrying."
                operationError = error.localizedDescription
                if selectedSessionID == session.id {
                    composer = composer.isEmpty ? message : message + "\n\n" + composer
                } else {
                    let newer = drafts[session.id] ?? ""
                    drafts[session.id] = newer.isEmpty ? message : message + "\n\n" + newer
                }
            }
        }
    }

    func action(_ action: String, session: SessionState, model: String? = nil) {
        guard let api, !pendingControls.contains(session.id) else { return }
        pendingControls.insert(session.id)
        Task {
            defer { pendingControls.remove(session.id) }
            do {
                try await api.action(action, sessionID: session.id, model: model)
                await refresh(fresh: true)
                await refreshSession(session.id)
            } catch {
                operationError = error.localizedDescription
            }
        }
    }

    func unpair() {
        guard let existingAPI = api else { return }
        let relay = pushRelayURL.map { PushRelayClient(baseURL: $0, session: networkSession) }
        let registration = storedPushRegistration()
        Task {
            do {
                try? await existingAPI.removePush()
                try await existingAPI.revokeCurrentDevice()
                if let relay, let registration { try? await relay.revoke(registration) }
                clearPairing()
            } catch {
                operationError = nil
                showForgetMacConfirmation = true
            }
        }
    }

    func forgetThisMac() {
        let relay = pushRelayURL.map { PushRelayClient(baseURL: $0, session: networkSession) }
        let registration = storedPushRegistration()
        clearPairing()
        Task {
            if let relay, let registration { try? await relay.revoke(registration) }
        }
    }

    private func clearPairing() {
        KeychainStore.removeAll()
        connectionTask?.cancel()
        eventTask?.cancel()
        eventRefreshTask?.cancel()
        catalogStreamTask?.cancel()
        sessionStreamTask?.cancel()
        sessionMetadataTask?.cancel()
        api = nil
        endpoint = nil
        token = nil
        pushRelayURL = nil
        snapshot = nil
        selectedDetail = nil
        selectedSessionID = nil
        composer = ""
        drafts = [:]
        olderActivity = []
        activityCursor = nil
        searchResults = []
        deliveryStatus = [:]
        deliveryQueueIDs = [:]
        lastEventID = 0
        catalogStreamCursor = 0
        catalogHostEpoch = nil
        connectionError = nil
        reconnecting = false
    }

    func requestNotifications() {
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound, .badge]) { granted, _ in
            if granted {
                DispatchQueue.main.async { UIApplication.shared.registerForRemoteNotifications() }
            } else {
                Task { @MainActor in await self.disableNotifications(status: "Not allowed") }
            }
        }
    }

    func openNotificationSettings() {
        guard let url = URL(string: UIApplication.openNotificationSettingsURLString) else { return }
        UIApplication.shared.open(url)
    }

    func turnOffNotifications() {
        Task { await disableNotifications(status: "Not enabled") }
    }

    func refreshNotificationRegistration() {
        UNUserNotificationCenter.current().getNotificationSettings { settings in
            let authorization = settings.authorizationStatus.rawValue
            Task { @MainActor in
                switch UNAuthorizationStatus(rawValue: authorization) {
                case .authorized, .provisional, .ephemeral:
                    UIApplication.shared.registerForRemoteNotifications()
                case .denied:
                    await self.disableNotifications(status: "Not allowed")
                default:
                    await self.disableNotifications(status: "Not enabled")
                }
            }
        }
    }

    func registerPushToken(_ deviceToken: String) {
        guard let api, let pushRelayURL else {
            notificationStatus = "Reconnect to finish setup"
            return
        }
        notificationStatus = "Enabling"
        Task {
            do {
                let relay = PushRelayClient(baseURL: pushRelayURL, session: networkSession)
                if let registration = storedPushRegistration(),
                   !registration.needsRenewal,
                   KeychainStore.get("pushDeviceToken") == deviceToken,
                   KeychainStore.get("pushEnvironment") == PushRelayClient.environment,
                   KeychainStore.get("pushEndpoint") == endpoint?.absoluteString {
                    do {
                        try await relay.validate(registration)
                        try await api.configurePush(installationID: registration.installationId, credential: registration.credential)
                        notificationStatus = "Enabled"
                        return
                    } catch AgenthailAPIError.request(let status, _) where status == 401 {
                        clearStoredPushRegistration()
                    }
                }
                if let previous = storedPushRegistration() { try? await relay.revoke(previous) }
                let registration = try await relay.register(deviceToken: deviceToken)
                let data = try JSONEncoder().encode(registration)
                try KeychainStore.set(data.base64EncodedString(), account: "pushRegistration")
                try KeychainStore.set(deviceToken, account: "pushDeviceToken")
                try KeychainStore.set(PushRelayClient.environment, account: "pushEnvironment")
                try KeychainStore.set(endpoint?.absoluteString ?? "", account: "pushEndpoint")
                try await api.configurePush(installationID: registration.installationId, credential: registration.credential)
                notificationStatus = "Enabled"
            } catch {
                notificationStatus = "Setup failed"
                operationError = error.localizedDescription
            }
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
                        recordStreamInterruption(probeError: nil)
                    } catch {
                        recordStreamInterruption(probeError: error)
                    }
                    let delay = backoff.nextDelay()
                    try? await Task.sleep(for: .seconds(delay))
                }
            }
        }
    }

    private func startCatalogStream() {
        catalogStreamTask?.cancel()
        guard let api else { return }
        catalogStreamTask = Task {
            let backoff = EventRetryBackoff()
            while !Task.isCancelled {
                do {
                    try await api.streamCatalog(after: catalogStreamCursor, onConnected: {}, onEvent: { [weak self] event in
                        backoff.reset()
                        await self?.receiveCatalog(event)
                    })
                } catch {
                    if Task.isCancelled { return }
                    if case AgenthailAPIError.streamGap = error {
                        catalogStreamCursor = 0
                        _ = await refresh(fresh: true)
                        continue
                    }
                    let delay = backoff.nextDelay()
                    try? await Task.sleep(for: .seconds(delay))
                }
            }
        }
    }

    func receiveCatalog(_ event: CatalogStreamEvent) async {
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
            snapshot = current
        case "session.removed":
            guard let id = event.data.sessionId else { _ = await refresh(); return }
            current.sessions.removeAll { $0.id == id }
            current.totalSessions = current.sessions.count
            snapshot = current
        case "surface.health":
            guard let name = event.data.surface, let health = event.data.health, let index = current.surfaces.firstIndex(where: { $0.name == name }) else { return }
            let previous = current.surfaces[index]
            current.surfaces[index] = SurfaceState(name: name, connected: health == "healthy", error: health == "healthy" ? nil : event.data.detail, health: health, healthDetail: event.data.detail, capabilities: previous.capabilities)
            snapshot = current
        default:
            return
        }
    }

    private func startSessionStream(_ id: String) {
        sessionStreamTask?.cancel()
        guard let api, selectedSessionID == id else { return }
        sessionStreamTask = Task {
            let backoff = EventRetryBackoff()
            while !Task.isCancelled, selectedSessionID == id {
                do {
                    try await api.streamSession(id: id, after: sessionStreamCursor, onConnected: {}, onEvent: { [weak self] event in
                        backoff.reset()
                        await self?.applySessionStreamEvent(event)
                    })
                } catch {
                    if Task.isCancelled || selectedSessionID != id { return }
                    if case AgenthailAPIError.streamGap = error {
                        sessionStreamCursor = 0
                        await refreshSession(id)
                        continue
                    }
                    let delay = backoff.nextDelay()
                    try? await Task.sleep(for: .seconds(delay))
                }
            }
        }
    }

    func applySessionStreamEvent(_ event: SessionStreamEvent) {
        guard event.stream == "session", event.sessionId == selectedSessionID, var detail = selectedDetail, detail.session.id == event.sessionId else { return }
        sessionStreamCursor = max(sessionStreamCursor, event.seq)
        if event.data.kind == "context" {
            detail.context = event.data.context
            sessionStreamMetadataFields.insert("context")
        } else if event.data.kind == "goal" {
            detail.goal = event.data.goal
            sessionStreamMetadataFields.insert("goal")
        }
        selectedDetail = detail
        if event.data.op == "reset", event.data.kind == "source-error" {
            sessionError = event.data.reason ?? "The session source restarted."
            return
        }
        guard var timeline = detail.timeline else { return }
        guard !event.data.itemId.isEmpty else { return }
        let item = TimelineItem(id: event.data.itemId, kind: event.data.kind, role: event.data.role, title: event.data.title ?? event.data.kind, text: event.data.body ?? "", timestamp: event.data.ts, callId: event.data.turnId, status: event.data.status, truncated: event.data.truncated, bodyRef: event.data.bodyRef)
        if event.data.op == "remove" {
            timeline.items.removeAll { $0.id == event.data.itemId }
        } else if let index = timeline.items.firstIndex(where: { $0.id == event.data.itemId }) {
            timeline.items[index] = item
        } else {
            timeline.items.append(item)
        }
        detail.timeline = timeline
        selectedDetail = detail
        sessionError = nil
    }

    func retainedSessionBody(for item: TimelineItem, start: Int = 0) async -> String? {
        guard let api, let id = selectedSessionID, let ref = item.bodyRef else { return nil }
        return try? await api.sessionStreamBody(id: id, ref: ref, start: start).body
    }

    private func receive(_ event: AgenthailEvent) async {
        lastEventID = event.type == "stream.reset" ? 0 : max(lastEventID, event.id)
        guard Self.shouldRefreshSnapshot(for: event.type) else { return }
        eventRefreshTask?.cancel()
        eventRefreshTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(200))
            guard !Task.isCancelled, let self else { return }
            await self.refresh(fresh: event.type == "stream.reset")
        }
    }

    func eventStreamConnected() async {
        reconnecting = false
        _ = await refresh(fresh: true)
        if let id = selectedSessionID { await refreshSession(id) }
        startCatalogStream()
        if let id = selectedSessionID { startSessionStream(id) }
    }

    func recordStreamInterruption(probeError: Error?) {
        reconnecting = probeError == nil
        connectionError = probeError?.localizedDescription
    }

    private func storedPushRegistration() -> PushRegistration? {
        guard let value = KeychainStore.get("pushRegistration"), let data = Data(base64Encoded: value) else { return nil }
        return try? JSONDecoder().decode(PushRegistration.self, from: data)
    }

    private func disableNotifications(status: String) async {
        guard let api else {
            notificationStatus = status
            return
        }
        do {
            try await api.removePush()
            if let pushRelayURL, let registration = storedPushRegistration() {
                try? await PushRelayClient(baseURL: pushRelayURL, session: networkSession).revoke(registration)
            }
            clearStoredPushRegistration()
            notificationStatus = status
        } catch {
            notificationStatus = status
            operationError = "Could not disable notification delivery. Reconnect and try again. \(error.localizedDescription)"
        }
    }

    private func clearStoredPushRegistration() {
        for account in ["pushRegistration", "pushDeviceToken", "pushEnvironment", "pushEndpoint"] {
            KeychainStore.remove(account)
        }
    }
}
