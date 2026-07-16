import Foundation
import UIKit
import UserNotifications

@MainActor
final class AgenthailIOSModel: ObservableObject {
    @Published var snapshot: DashboardSnapshot?
    @Published var selectedDetail: SessionDetail?
    @Published var selectedSessionID: String?
    @Published var connectionError: String?
    @Published var operationError: String?
    @Published var pairing = false
    @Published var composer = ""
    @Published var notificationStatus = "Not enabled"
    @Published var requestedSessionID: String?
    @Published var showForgetMacConfirmation = false
    @Published var showPairingConfirmation = false

    private var api: AgenthailAPI?
    private var endpoint: URL?
    private var token: String?
    private var eventTask: Task<Void, Never>?
    private var eventRefreshTask: Task<Void, Never>?
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

    deinit {
        connectionTask?.cancel()
        eventTask?.cancel()
        eventRefreshTask?.cancel()
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
        connectionTask = Task {
            do {
                let version = try await api.version()
                guard !Task.isCancelled else { return }
                if let value = version.pushRelayUrl, let url = URL(string: value) {
                    pushRelayURL = url
                    try? KeychainStore.set(value, account: "pushRelayURL")
                } else if let value = KeychainStore.get("pushRelayURL") {
                    pushRelayURL = URL(string: value)
                }
                guard await refresh(fresh: true) else { return }
                guard !Task.isCancelled else { return }
                startEvents()
                refreshNotificationRegistration()
            } catch {
                connectionError = error.localizedDescription
            }
        }
    }

    @discardableResult
    func refresh(fresh: Bool = false) async -> Bool {
        guard let api else { return false }
        do {
            let loaded = try await api.snapshot(fresh: fresh)
            snapshot = loaded
            lastEventID = max(lastEventID, loaded.eventCursor ?? lastEventID)
            connectionError = nil
            return true
        } catch {
            connectionError = error.localizedDescription
            return false
        }
    }

    func loadSession(_ id: String) async {
        selectedSessionID = id
        selectedDetail = nil
        await refreshSession(id)
    }

    private func refreshSession(_ id: String) async {
        guard let api, sessionLoadIsCurrent(id, selectedID: selectedSessionID) else { return }
        do {
            let detail = try await api.sessionDetail(id: id)
            guard sessionLoadIsCurrent(id, selectedID: selectedSessionID) else { return }
            selectedDetail = detail
            operationError = nil
        } catch {
            guard sessionLoadIsCurrent(id, selectedID: selectedSessionID) else { return }
            operationError = error.localizedDescription
        }
    }

    func openNotification(_ sessionID: String) {
        requestedSessionID = sessionID
    }

    func send(to session: SessionState) {
        let message = composer.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !message.isEmpty, let api else { return }
        composer = ""
        Task {
            do {
                try await api.action(session.isWorking ? "steer" : "send", sessionID: session.id, message: message)
                await refreshSession(session.id)
            } catch {
                operationError = error.localizedDescription
                if composer.isEmpty { composer = message }
            }
        }
    }

    func action(_ action: String, session: SessionState, model: String? = nil) {
        guard let api else { return }
        Task {
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
        api = nil
        endpoint = nil
        token = nil
        pushRelayURL = nil
        snapshot = nil
        selectedDetail = nil
        selectedSessionID = nil
        lastEventID = 0
        connectionError = nil
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
                let relay = PushRelayClient(baseURL: pushRelayURL)
                if let registration = storedPushRegistration(),
                   !registration.needsRenewal,
                   KeychainStore.get("pushDeviceToken") == deviceToken,
                   KeychainStore.get("pushEnvironment") == PushRelayClient.environment,
                   KeychainStore.get("pushEndpoint") == endpoint?.absoluteString {
                    try await api.configurePush(installationID: registration.installationId, credential: registration.credential)
                    notificationStatus = "Enabled"
                    return
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
                    connectionError = error.localizedDescription
                    let delay = backoff.nextDelay()
                    try? await Task.sleep(for: .seconds(delay))
                }
            }
        }
    }

    private func receive(_ event: AgenthailEvent) async {
        lastEventID = event.type == "stream.reset" ? 0 : max(lastEventID, event.id)
        let selectedID = selectedSessionID
        eventRefreshTask?.cancel()
        eventRefreshTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(200))
            guard !Task.isCancelled, let self else { return }
            await self.refresh(fresh: true)
            if let selectedID, sessionLoadIsCurrent(selectedID, selectedID: self.selectedSessionID) {
                await self.refreshSession(selectedID)
            }
        }
    }

    private func eventStreamConnected() {
        connectionError = nil
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
