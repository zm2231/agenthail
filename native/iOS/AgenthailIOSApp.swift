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

    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification) async -> UNNotificationPresentationOptions {
        [.banner, .sound, .badge]
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse) async {
        let userInfo = response.notification.request.content.userInfo
        guard let sessionID = userInfo["sessionId"] as? String, !sessionID.isEmpty,
              let host = userInfo["host"] as? String, !host.isEmpty else { return }
        if let choice = RequestNotifications.choice(forAction: response.actionIdentifier), let requestID = userInfo["requestId"] as? String {
            await RequestAnswer.send(choice: choice, text: (response as? UNTextInputNotificationResponse)?.userText, sessionID: sessionID, requestID: requestID, api: { @Sendable in try Self.pairedAPI(host: host) })
            return
        }
        let opened = HostCatalog.qualify(host, sessionID)
        await MainActor.run {
            NotificationCenter.default.post(name: .agenthailNotificationOpened, object: opened)
        }
    }
}

extension IOSAppDelegate {
    nonisolated static func pairedAPI(host: String) throws -> AgenthailAPI {
        guard let credential = KeychainStore.get(PairedCredential.account(for: host)).flatMap(PairedCredential.decode) else {
            throw AgenthailAPIError.unavailable("This iPhone is no longer paired with the computer that sent this notification.")
        }
        return AgenthailAPI(baseURL: credential.endpoint, token: credential.token)
    }
}

extension Notification.Name {
    static let agenthailPushToken = Notification.Name("AgenthailPushToken")
    static let agenthailPushRegistrationFailed = Notification.Name("AgenthailPushRegistrationFailed")
    static let agenthailNotificationOpened = Notification.Name("AgenthailNotificationOpened")
}

@main
struct AgenthailIOSApp: App {
    @UIApplicationDelegateAdaptor(IOSAppDelegate.self) private var appDelegate
    @StateObject private var model = AgenthailIOSModel(autoConnect: !sessionPreviewEnabled)
    @StateObject private var voiceCalls = VoiceCallCoordinator()
    @Environment(\.scenePhase) private var scenePhase
    @State private var wasInBackground = false

    var body: some Scene {
        WindowGroup {
            Group {
#if DEBUG
                if ProcessInfo.processInfo.arguments.contains("--preview-voice") {
                    AgenthailVoiceOperatorSheet(model: VoiceOperatorModel(preview: true), rowMemory: TranscriptRowMemory(), openSession: { _ in })
                } else if ProcessInfo.processInfo.arguments.contains("--preview-session") {
                    SessionPreview()
                } else { connectedRoot }
#else
                connectedRoot
#endif
            }
            .environmentObject(voiceCalls)
        }
    }
    private var connectedRoot: some View {
        AgenthailIOSRoot(model: model)
            .onOpenURL { model.handlePairingURL($0) }
            .onReceive(NotificationCenter.default.publisher(for: .agenthailPushToken)) { notification in
                if let token = notification.object as? String { model.registerPushToken(token) }
            }
            .onReceive(NotificationCenter.default.publisher(for: .agenthailPushRegistrationFailed)) { notification in
                model.notificationStatus = "Setup failed"
                model.operationError = notification.object as? String ?? "This device could not register for notifications."
            }
            .onChange(of: scenePhase) { _, phase in
                if phase == .background {
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
