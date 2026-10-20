import SwiftUI
import UIKit
import UserNotifications

final class IOSAppDelegate: NSObject, UIApplicationDelegate, @preconcurrency UNUserNotificationCenterDelegate {
    func application(_ application: UIApplication, didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil) -> Bool {
        UNUserNotificationCenter.current().delegate = self
        KeychainStore.shareCredential()
        return true
    }

    func application(_ application: UIApplication, didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data) {
        NotificationCenter.default.post(name: .agenthailPushToken, object: deviceToken.map { String(format: "%02x", $0) }.joined())
    }

    func application(_ application: UIApplication, didFailToRegisterForRemoteNotificationsWithError error: Error) {
        NotificationCenter.default.post(name: .agenthailPushRegistrationFailed, object: error.localizedDescription)
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification) async -> UNNotificationPresentationOptions {
        [.banner, .sound, .badge]
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse) async {
        let userInfo = response.notification.request.content.userInfo
        guard let sessionID = userInfo["sessionId"] as? String, !sessionID.isEmpty else { return }
        if let choice = RequestNotifications.choice(forAction: response.actionIdentifier), let requestID = userInfo["requestId"] as? String {
            await RequestAnswer.send(choice: choice, text: (response as? UNTextInputNotificationResponse)?.userText, sessionID: sessionID, requestID: requestID, api: Self.pairedAPI)
            return
        }
        await MainActor.run {
            NotificationCenter.default.post(name: .agenthailNotificationOpened, object: sessionID)
        }
    }
}

extension IOSAppDelegate {
    nonisolated static func pairedAPI() throws -> AgenthailAPI {
        guard let endpoint = KeychainStore.get("endpoint").flatMap(URL.init(string:)), let token = KeychainStore.get("token") else {
            throw AgenthailAPIError.unavailable("Pair this phone with Agenthail on your Mac first.")
        }
        return AgenthailAPI(baseURL: endpoint, token: token)
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

    var body: some Scene {
        WindowGroup {
            Group {
#if DEBUG
                if ProcessInfo.processInfo.arguments.contains("--preview-voice") {
                    AgenthailVoiceOperatorSheet(model: VoiceOperatorModel(preview: true), openSession: { _ in })
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
                if phase == .active { model.resumeConnection() }
            }
            .onChange(of: model.isPaired) { _, paired in
                if !paired { voiceCalls.disconnect() }
            }
    }

}

private var sessionPreviewEnabled: Bool {
#if DEBUG
    ProcessInfo.processInfo.arguments.contains("--preview-session") || ProcessInfo.processInfo.arguments.contains("--preview-voice")
#else
    false
#endif
}
