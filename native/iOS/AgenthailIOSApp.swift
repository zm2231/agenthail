import SwiftUI
import UIKit
import UserNotifications

final class IOSAppDelegate: NSObject, UIApplicationDelegate, @preconcurrency UNUserNotificationCenterDelegate {
    func application(_ application: UIApplication, didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil) -> Bool {
        UNUserNotificationCenter.current().delegate = self
        return true
    }

    func application(_ application: UIApplication, didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data) {
        NotificationCenter.default.post(name: .agenthailPushToken, object: deviceToken.map { String(format: "%02x", $0) }.joined())
    }

    func application(_ application: UIApplication, didFailToRegisterForRemoteNotificationsWithError error: Error) {
        NotificationCenter.default.post(name: .agenthailPushRegistrationFailed, object: error.localizedDescription)
    }

    func application(_ application: UIApplication, didReceiveRemoteNotification userInfo: [AnyHashable: Any]) async -> UIBackgroundFetchResult {
        guard let key = userInfo["withdraw"] as? String, !key.isEmpty,
              let host = userInfo["host"] as? String, !host.isEmpty else { return .noData }
        let center = UNUserNotificationCenter.current()
        let withdrawn = await center.deliveredNotifications()
            .filter { $0.request.content.userInfo["host"] as? String == host }
            .filter { $0.request.identifier == key || $0.request.content.userInfo["collapseId"] as? String == key }
            .map(\.request.identifier)
        guard !withdrawn.isEmpty else { return .noData }
        center.removeDeliveredNotifications(withIdentifiers: withdrawn)
        return .newData
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification) async -> UNNotificationPresentationOptions {
        Self.presentation(notification.request.content.userInfo, onScreen: OnScreenSessions.shared.contains)
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse) async {
        let userInfo = response.notification.request.content.userInfo
        guard let sessionID = userInfo["sessionId"] as? String, !sessionID.isEmpty,
              let host = userInfo["host"] as? String, !host.isEmpty else { return }
        if let choice = RequestNotifications.choice(forAction: response.actionIdentifier, category: response.notification.request.content.categoryIdentifier), let requestID = userInfo["requestId"] as? String {
            await RequestAnswer.send(choice: choice, text: (response as? UNTextInputNotificationResponse)?.userText, sessionID: sessionID, host: host, requestID: requestID, api: { @Sendable in try Self.pairedAPI(host: host) })
            return
        }
        guard let opened = Self.openedSession(userInfo) else { return }
        let turn = Self.openedTurn(userInfo)
        await MainActor.run { PhoneRoute.shared.open(OpenedNotification(session: opened, turn: turn)) }
    }
}

extension IOSAppDelegate {
    nonisolated static func openedSession(_ userInfo: [AnyHashable: Any]) -> String? {
        guard let sessionID = userInfo["sessionId"] as? String, !sessionID.isEmpty,
              let host = userInfo["host"] as? String, !host.isEmpty else { return nil }
        return HostCatalog.qualify(host, sessionID)
    }

    nonisolated static func presentation(_ userInfo: [AnyHashable: Any], onScreen: (String) -> Bool) -> UNNotificationPresentationOptions {
        if let session = openedSession(userInfo), onScreen(session) { return [.badge] }
        return [.banner, .sound, .badge]
    }

    nonisolated static func openedTurn(_ userInfo: [AnyHashable: Any]) -> String? {
        guard let turn = userInfo["turnId"] as? String, !turn.isEmpty else { return nil }
        return turn
    }

    nonisolated static func pairedAPI(host: String) throws -> AgenthailAPI {
        guard let credential = KeychainStore.get(PairedCredential.account(for: host)).flatMap(PairedCredential.decode) else {
            throw AgenthailAPIError.unavailable("This iPhone is no longer paired with the computer that sent this notification.")
        }
        return AgenthailAPI(baseURL: credential.endpoint, token: credential.token)
    }
}

struct OpenedNotification: Sendable, Equatable {
    let id = UUID()
    let session: String
    let turn: String?
}

@MainActor
@Observable
final class PhoneRoute {
    static let shared = PhoneRoute()
    private(set) var pending: OpenedNotification?

    func open(_ opened: OpenedNotification) {
        pending = opened
    }

    func take() -> OpenedNotification? {
        defer { pending = nil }
        return pending
    }
}

final class OnScreenSessions: @unchecked Sendable {
    static let shared = OnScreenSessions()
    private let lock = NSLock()
    private var counts: [String: Int] = [:]

    func appeared(_ id: String) {
        lock.withLock { counts[id, default: 0] += 1 }
    }

    func disappeared(_ id: String) {
        lock.withLock {
            guard let count = counts[id] else { return }
            counts[id] = count > 1 ? count - 1 : nil
        }
    }

    func contains(_ id: String) -> Bool {
        lock.withLock { counts[id] != nil }
    }
}

extension Notification.Name {
    static let agenthailPushToken = Notification.Name("AgenthailPushToken")
    static let agenthailPushRegistrationFailed = Notification.Name("AgenthailPushRegistrationFailed")
}

@main
struct AgenthailIOSApp: App {
    @UIApplicationDelegateAdaptor(IOSAppDelegate.self) private var appDelegate
    @State private var model = AgenthailIOSModel(autoConnect: !sessionPreviewEnabled, composerStore: sessionPreviewEnabled ? nil : .standard(protectedDataIsAvailable: { UIApplication.shared.isProtectedDataAvailable }), onScreen: .shared)
    @State private var voiceCalls = VoiceCallCoordinator(makeModel: { Self.voiceModel() })
    @Environment(\.scenePhase) private var scenePhase
    @State private var wasInBackground = false

    init() {
        Task.detached(priority: .utility) { SentFileCache.standard.prune() }
    }

    var body: some Scene {
        WindowGroup {
            Group {
#if DEBUG
                if ProcessInfo.processInfo.arguments.contains("--preview-voice") {
                    AgenthailVoiceOperatorSheet(model: VoiceOperatorModel(preview: true), rowMemory: TranscriptRowMemory(), openSession: { _ in })
                } else if ProcessInfo.processInfo.arguments.contains("--preview-session") {
                    SessionPreview().overlay(alignment: .topLeading) {
                        SceneEvaluationStamp(count: SceneEvaluations.next())
                        if BodyRuns.enabled { BodyRunsProbe() }
                    }
                } else { connectedRoot }
#else
                connectedRoot
#endif
            }
            .environmentObject(voiceCalls)
        }
    }

    private static func voiceModel() -> VoiceOperatorModel {
#if DEBUG
        if sessionPreviewEnabled { return PreviewVoice.model() }
#endif
        return VoiceOperatorModel.paired()
    }

    private var connectedRoot: some View {
        AgenthailIOSRoot(model: model)
            .onOpenURL { model.handlePairingURL($0) }
            .onReceive(NotificationCenter.default.publisher(for: .agenthailPushToken)) { notification in
                if let token = notification.object as? String { model.push.registerPushToken(token) }
            }
            .onReceive(NotificationCenter.default.publisher(for: .agenthailPushRegistrationFailed)) { notification in
                model.push.pushRegistrationFailed(notification.object as? String ?? "This device could not register for notifications.")
            }
            .onChange(of: scenePhase) { _, phase in
                model.setAppActive(phase == .active)
                if phase == .active {
                    ConnectionDrops.shared.foregrounded()
                    Task.detached { await PushReceipts.flush() }
                }
                if phase == .background {
                    model.saveComposerState()
                    ConnectionDrops.shared.backgrounded()
                    wasInBackground = true
                } else if phase == .active, wasInBackground {
                    wasInBackground = false
                    model.resumeConnection()
                }
            }
            .onChange(of: model.isPaired) { _, paired in
                if !paired { voiceCalls.disconnect() }
            }
            .onChange(of: model.voiceHost?.id) { _, _ in voiceCalls.disconnect() }
    }

}

private var sessionPreviewEnabled: Bool {
#if DEBUG
    ProcessInfo.processInfo.arguments.contains("--preview-session") || ProcessInfo.processInfo.arguments.contains("--preview-voice")
#else
    false
#endif
}
