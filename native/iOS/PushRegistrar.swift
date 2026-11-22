import Foundation
import UIKit
import UserNotifications

@MainActor
@Observable
final class PushRegistrar {
    private struct StoredPush: Codable {
        let registration: PushRegistration
        let deviceToken: String
        let environment: String
        let endpoint: String
    }
    var notificationStatus = "Not enabled"
    var notificationPreferences: [NotificationPreferenceState] = []
    var notificationPreferenceProblem: String?
    var notificationProblems: [String] = []
    @ObservationIgnored private var pushDeviceToken: String?
    @ObservationIgnored private var pushStatus: [String: String] = [:]
    @ObservationIgnored private var pushRetries: [String: (id: UUID, connection: ObjectIdentifier, task: Task<Void, Never>)] = [:]
    @ObservationIgnored private var pushFailures: [String: String] = [:]
    @ObservationIgnored private var deviceRegistrationFailure: String?
    @ObservationIgnored private var appActive = true
    @ObservationIgnored private var repairingPush: Set<String> = []
    @ObservationIgnored private let notificationDefaults: UserDefaults
    private static let notificationsOffKey = "notificationsTurnedOff"
    private var pushTurnedOff: Bool {
        get { notificationDefaults.bool(forKey: Self.notificationsOffKey) }
        set { notificationDefaults.set(newValue, forKey: Self.notificationsOffKey) }
    }
    @ObservationIgnored private let networkSession: URLSession
    @ObservationIgnored private let hostConnection: (String) -> HostConnection?
    @ObservationIgnored private let order: () -> [String]
    @ObservationIgnored private let changed: () -> Void
    @ObservationIgnored private let authorization: () async -> UNAuthorizationStatus
    private var hostOrder: [String] { order() }

    nonisolated static func systemAuthorization() async -> UNAuthorizationStatus {
        await UNUserNotificationCenter.current().notificationSettings().authorizationStatus
    }

    init(defaults: UserDefaults, session: URLSession, connection: @escaping (String) -> HostConnection?, order: @escaping () -> [String], changed: @escaping () -> Void, authorization: @escaping () async -> UNAuthorizationStatus = PushRegistrar.systemAuthorization) {
        notificationDefaults = defaults
        networkSession = session
        hostConnection = connection
        self.order = order
        self.changed = changed
        self.authorization = authorization
    }

    func status(for hostID: String) -> String { pushStatus[hostID] ?? "Not enabled" }

    func storedRegistration(_ hostID: String) -> PushRegistration? { storedPush(hostID)?.registration }

    func setAppActive(_ active: Bool) {
        appActive = active
        if !active {
            for id in Array(pushRetries.keys) { cancelPushSetup(id) }
            return
        }
        guard let pushDeviceToken else { return }
        for id in hostOrder where pushStatus[id] != "Enabled" {
            if let connection = hostConnection(id), connection.reachable { registerPush(connection, deviceToken: pushDeviceToken) }
        }
    }

    func hostConnected(_ host: HostConnection) {
        if let pushDeviceToken { registerPush(host, deviceToken: pushDeviceToken) }
        else { refreshNotificationRegistration() }
    }

    func hostUnreachable(_ hostID: String) {
        cancelPushSetup(hostID)
    }

    func stopSetup(_ hostID: String) {
        cancelPushSetup(hostID)
        pushFailures.removeValue(forKey: hostID)
    }

    func forget(_ hostID: String) {
        clearStoredPush(hostID)
        pushStatus.removeValue(forKey: hostID)
    }

    func beginRepair(_ hostID: String) async {
        repairingPush.insert(hostID)
        await cancelPushSetup(hostID)?.value
    }

    func revokeEarlierRegistration(_ hostID: String, relayURL: URL) async -> String? {
        guard let stored = storedPush(hostID) else { return nil }
        return await revokeBeforeRepair(PushRelayClient(baseURL: relayURL, session: networkSession), stored.registration)
    }

    func finishRepair(_ hostID: String) {
        repairingPush.remove(hostID)
    }

    func abandonRepair(_ hostID: String) {
        guard repairingPush.remove(hostID) != nil, let pushDeviceToken, let previous = hostConnection(hostID), previous.reachable else { return }
        registerPush(previous, deviceToken: pushDeviceToken)
    }

    func requestNotifications() {
        pushTurnedOff = false
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
        pushTurnedOff = true
        Task { await disableNotifications(status: "Not enabled") }
    }

    func refreshNotificationRegistration() {
        Task { @MainActor in
            switch await authorization() {
            case .authorized, .provisional, .ephemeral:
                UIApplication.shared.registerForRemoteNotifications()
            case .denied:
                await self.disableNotifications(status: "Not allowed")
            default:
                await self.disableNotifications(status: "Not enabled")
            }
        }
    }

    func showNotificationAuthorization() {
        Task { @MainActor in
            switch await authorization() {
            case .denied: self.notificationStatus = "Not allowed"
            case .notDetermined: self.notificationStatus = "Not enabled"
            default: break
            }
        }
    }

    func registerPushToken(_ deviceToken: String) {
        pushDeviceToken = deviceToken
        deviceRegistrationFailure = nil
        for id in hostOrder {
            if let connection = hostConnection(id), connection.reachable { registerPush(connection, deviceToken: deviceToken) }
        }
        updateNotificationStatus()
    }

    func pushRegistrationFailed(_ reason: String) {
        deviceRegistrationFailure = reason
        updateNotificationStatus()
    }

    private func revokeBeforeRepair(_ relay: PushRelayClient, _ registration: PushRegistration) async -> String? {
        var failure: Error
        do { try await relay.revoke(registration); return nil } catch { failure = error }
        if case let .transient(retryAfter) = PushSetupFailure.classify(failure),
           let delay = PushSetupFailure.delay(afterFailure: 1, retryAfter: retryAfter) {
            try? await Task.sleep(for: .seconds(delay))
            do { try await relay.revoke(registration); return nil } catch { failure = error }
        }
        if case .transient = PushSetupFailure.classify(failure) {
            return "Couldn't reach the notification relay to remove this iPhone's earlier registration, so the computer was not paired again. Check the connection and pair again."
        }
        return "The notification relay would not remove this iPhone's earlier registration, so the computer was not paired again: \(failure.localizedDescription)"
    }

    @discardableResult
    private func cancelPushSetup(_ hostID: String) -> Task<Void, Never>? {
        guard let retry = pushRetries.removeValue(forKey: hostID) else { return nil }
        retry.task.cancel()
        if pushStatus[hostID] == "Enabling" { pushStatus[hostID] = "Reconnect to finish setup" }
        updateNotificationStatus()
        return retry.task
    }

    private func registerPush(_ connection: HostConnection, deviceToken: String) {
        let hostID = connection.id
        guard !connection.isPreview, appActive, !pushTurnedOff, !repairingPush.contains(hostID) else { return }
        if let running = pushRetries[hostID] {
            if running.connection == ObjectIdentifier(connection) { return }
            cancelPushSetup(hostID)
        }
        guard let relayURL = connection.pushRelayURL else {
            pushStatus[hostID] = "Reconnect to finish setup"
            updateNotificationStatus()
            return
        }
        pushStatus[hostID] = "Enabling"
        pushFailures[hostID] = nil
        updateNotificationStatus()
        let id = UUID()
        let task = Task {
            defer {
                if pushRetries[hostID]?.id == id { pushRetries[hostID] = nil }
                updateNotificationStatus()
            }
            var failures = 0
            while true {
                do {
                    try await setUpPush(connection, relayURL: relayURL, deviceToken: deviceToken)
                    return
                } catch {
                    guard !Task.isCancelled, hostConnection(hostID) === connection else { return }
                    failures += 1
                    if case let .transient(retryAfter) = PushSetupFailure.classify(error),
                       let delay = PushSetupFailure.delay(afterFailure: failures, retryAfter: retryAfter) {
                        try? await Task.sleep(for: .seconds(delay))
                        guard !Task.isCancelled, appActive, connection.reachable, hostConnection(hostID) === connection else { return }
                        continue
                    }
                    pushStatus[hostID] = "Setup failed"
                    pushFailures[hostID] = error.localizedDescription
                    return
                }
            }
        }
        pushRetries[hostID] = (id, ObjectIdentifier(connection), task)
    }

    private func setUpPush(_ connection: HostConnection, relayURL: URL, deviceToken: String) async throws {
        let hostID = connection.id
        let relay = PushRelayClient(baseURL: relayURL, session: networkSession)
        let endpoint = connection.credential.endpoint.absoluteString
        if let stored = storedPush(hostID), !stored.registration.needsRenewal, stored.deviceToken == deviceToken,
           stored.environment == PushRelayClient.environment, stored.endpoint == endpoint {
            do {
                try await relay.validate(stored.registration)
                try Task.checkCancellation()
                try await connection.api.configurePush(installationID: stored.registration.installationId, credential: stored.registration.credential)
                pushStatus[hostID] = "Enabled"
                return
            } catch AgenthailAPIError.request(let status, _) where status == 401 {
                clearStoredPush(hostID)
            }
        }
        if let previous = storedPush(hostID) { try? await relay.revoke(previous.registration) }
        let registration = try await relay.register(deviceToken: deviceToken, tag: hostID)
        let stored = StoredPush(registration: registration, deviceToken: deviceToken, environment: PushRelayClient.environment, endpoint: endpoint)
        try KeychainStore.set(String(decoding: try JSONEncoder().encode(stored), as: UTF8.self), account: Self.pushAccount(hostID))
        guard !Task.isCancelled, hostConnection(hostID) === connection else {
            try? await relay.revoke(registration)
            clearStoredPush(hostID)
            throw CancellationError()
        }
        try await connection.api.configurePush(installationID: registration.installationId, credential: registration.credential)
        pushStatus[hostID] = "Enabled"
    }

    func updateNotificationStatus() {
        notificationProblems = (deviceRegistrationFailure.map { [$0] } ?? [])
            + hostOrder.compactMap { id in pushFailures[id].map { "\(hostConnection(id)?.name ?? id): \($0)" } }
        let statuses = hostOrder.filter { !(hostConnection($0)?.isPreview ?? true) }.map { pushStatus[$0] ?? "Not enabled" }
        if deviceRegistrationFailure != nil { notificationStatus = "Setup failed" }
        if statuses.isEmpty { return }
        if deviceRegistrationFailure != nil || statuses.contains("Setup failed") { notificationStatus = "Setup failed" }
        else if statuses.contains("Enabling") { notificationStatus = "Enabling" }
        else if statuses.allSatisfy({ $0 == "Enabled" }) { notificationStatus = "Enabled" }
        else if statuses.contains("Enabled") || statuses.contains("Reconnect to finish setup") { notificationStatus = "Reconnect to finish setup" }
        else { notificationStatus = statuses.first ?? "Not enabled" }
        changed()
    }

    private static func pushAccount(_ hostID: String) -> String { "push:\(hostID)" }

    private func storedPush(_ hostID: String) -> StoredPush? {
        guard let value = KeychainStore.get(Self.pushAccount(hostID)) else { return nil }
        return try? JSONDecoder().decode(StoredPush.self, from: Data(value.utf8))
    }

    func loadNotificationPreferences() async {
        var merged: [NotificationPreferenceState] = []
        var failed: [String] = []
        for id in hostOrder {
            guard let connection = hostConnection(id), !connection.isPreview else { continue }
            do {
                for preference in try await connection.api.notificationPreferences() {
                    if let index = merged.firstIndex(where: { $0.id == preference.id }) {
                        let shown = merged[index]
                        merged[index] = NotificationPreferenceState(id: shown.id, title: shown.title, detail: shown.detail, value: shown.value && preference.value)
                    } else {
                        merged.append(preference)
                    }
                }
            } catch {
                failed.append(id)
            }
        }
        if !failed.isEmpty {
            merged = merged.map { NotificationPreferenceState(id: $0.id, title: $0.title, detail: $0.detail, value: false) }
        }
        notificationPreferences = merged
        notificationPreferenceProblem = failed.isEmpty ? nil : "Some computers could not be reached, so these show off until every computer confirms."
    }

    func setNotificationPreference(_ id: String, to value: Bool) {
        Task {
            var failed = false
            for hostID in hostOrder {
                guard let connection = hostConnection(hostID), !connection.isPreview else { continue }
                do { _ = try await connection.api.setNotificationPreference(id, to: value) } catch { failed = true }
            }
            await loadNotificationPreferences()
            if failed { notificationPreferenceProblem = "Some computers did not take the change. Reconnect and try again." }
        }
    }

    private func disableNotifications(status: String) async {
        for id in Array(pushRetries.keys) { await cancelPushSetup(id)?.value }
        deviceRegistrationFailure = nil
        for id in hostOrder {
            guard let connection = hostConnection(id), !connection.isPreview else { continue }
            pushFailures[id] = nil
            do {
                try await connection.api.removePush()
                if let relayURL = connection.pushRelayURL, let stored = storedPush(id) {
                    try? await PushRelayClient(baseURL: relayURL, session: networkSession).revoke(stored.registration)
                }
                clearStoredPush(id)
            } catch {
                pushFailures[id] = "Could not turn off delivery. Reconnect and try again. \(error.localizedDescription)"
            }
            pushStatus[id] = status
        }
        updateNotificationStatus()
        notificationStatus = status
    }

    func clearStoredPush(_ hostID: String) {
        KeychainStore.remove(Self.pushAccount(hostID))
    }
}
