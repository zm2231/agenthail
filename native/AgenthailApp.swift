import AppKit
import Combine
import ServiceManagement
import SwiftUI
import UserNotifications

private let notificationCategory = "AGENTHAIL_COMPLETION"
private let openDashboardAction = "OPEN_DASHBOARD"

private struct NotificationState: Codable {
    let available: Bool
    let authorization: String
    let authorized: Bool
    let alerts: Bool
    let sounds: Bool
    let error: String?
}

private enum NativeCommand {
    static func run(_ arguments: [String]) -> Int32 {
        guard let command = arguments.first else { return 64 }
        switch command {
        case "status":
            return printState(notificationState())
        case "request":
            return requestAuthorization()
        case "send":
            return send(arguments: Array(arguments.dropFirst()))
        case "settings":
            if openNotificationSettings() { return 0 }
            FileHandle.standardError.write(Data("unable to open macOS notification settings\n".utf8))
            return 1
        case "service":
            return manageService(Array(arguments.dropFirst()))
        default:
            FileHandle.standardError.write(Data("unknown Agenthail app command: \(command)\n".utf8))
            return 64
        }
    }

    private static func notificationState() -> NotificationState {
        let semaphore = DispatchSemaphore(value: 0)
        var state = NotificationState(available: true, authorization: "unknown", authorized: false, alerts: false, sounds: false, error: nil)
        UNUserNotificationCenter.current().getNotificationSettings { settings in
            state = NotificationState(
                available: true,
                authorization: authorizationName(settings.authorizationStatus),
                authorized: settings.authorizationStatus == .authorized || settings.authorizationStatus == .provisional,
                alerts: settings.alertSetting == .enabled,
                sounds: settings.soundSetting == .enabled,
                error: nil
            )
            semaphore.signal()
        }
        if semaphore.wait(timeout: .now() + 5) == .timedOut {
            return NotificationState(available: true, authorization: "unknown", authorized: false, alerts: false, sounds: false, error: "notification settings timed out")
        }
        return state
    }

    private static func requestAuthorization() -> Int32 {
        NSApplication.shared.setActivationPolicy(.accessory)
        NSApplication.shared.activate(ignoringOtherApps: true)
        let semaphore = DispatchSemaphore(value: 0)
        var requestError: Error?
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound]) { _, error in
            requestError = error
            semaphore.signal()
        }
        if !waitForSignal(semaphore, timeout: 120) {
            return printState(NotificationState(available: true, authorization: "unknown", authorized: false, alerts: false, sounds: false, error: "notification permission request timed out"))
        }
        if let requestError {
            return printState(NotificationState(available: true, authorization: "unknown", authorized: false, alerts: false, sounds: false, error: requestError.localizedDescription))
        }
        return printState(notificationState())
    }

    private static func waitForSignal(_ semaphore: DispatchSemaphore, timeout: TimeInterval) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if semaphore.wait(timeout: .now()) == .success { return true }
            _ = RunLoop.current.run(mode: .default, before: Date().addingTimeInterval(0.05))
        }
        return semaphore.wait(timeout: .now()) == .success
    }

    private static func send(arguments: [String]) -> Int32 {
        let values = parse(arguments)
        guard let title = values["title"], let message = values["message"] else {
            FileHandle.standardError.write(Data("send requires --title and --message\n".utf8))
            return 64
        }
        let center = UNUserNotificationCenter.current()
        let open = UNNotificationAction(identifier: openDashboardAction, title: "Open Agenthail")
        center.setNotificationCategories([UNNotificationCategory(identifier: notificationCategory, actions: [open], intentIdentifiers: [])])
        let content = UNMutableNotificationContent()
        content.title = title
        content.body = message
        content.sound = .default
        content.categoryIdentifier = notificationCategory
        let identifier = values["identifier"] ?? UUID().uuidString
        let request = UNNotificationRequest(identifier: identifier, content: content, trigger: nil)
        let semaphore = DispatchSemaphore(value: 0)
        var sendError: Error?
        center.add(request) { error in
            sendError = error
            semaphore.signal()
        }
        if semaphore.wait(timeout: .now() + 5) == .timedOut {
            FileHandle.standardError.write(Data("notification delivery timed out\n".utf8))
            return 1
        }
        if let sendError {
            FileHandle.standardError.write(Data("\(sendError.localizedDescription)\n".utf8))
            return 1
        }
        return 0
    }

    private static func parse(_ arguments: [String]) -> [String: String] {
        var values: [String: String] = [:]
        var index = 0
        while index + 1 < arguments.count {
            let key = arguments[index]
            if key.hasPrefix("--") {
                values[String(key.dropFirst(2))] = arguments[index + 1]
            }
            index += 2
        }
        return values
    }

    private static func printState(_ state: NotificationState) -> Int32 {
        let encoder = JSONEncoder()
        guard let data = try? encoder.encode(state) else { return 1 }
        FileHandle.standardOutput.write(data)
        FileHandle.standardOutput.write(Data("\n".utf8))
        return state.error == nil ? 0 : 1
    }

    private static func authorizationName(_ status: UNAuthorizationStatus) -> String {
        switch status {
        case .notDetermined: return "notDetermined"
        case .denied: return "denied"
        case .authorized: return "authorized"
        case .provisional: return "provisional"
        case .ephemeral: return "ephemeral"
        @unknown default: return "unknown"
        }
    }

    private static func openNotificationSettings() -> Bool {
        let urls = [
            "x-apple.systempreferences:com.apple.Notifications-Settings.extension?id=com.agenthail.app",
            "x-apple.systempreferences:com.apple.preference.notifications"
        ]
        for value in urls {
            if let url = URL(string: value), NSWorkspace.shared.open(url) { return true }
        }
        return false
    }

    private static func manageService(_ arguments: [String]) -> Int32 {
        guard let action = arguments.first else {
            FileHandle.standardError.write(Data("service requires enable, disable, status, or settings\n".utf8))
            return 64
        }
        let service = SMAppService.mainApp
        do {
            switch action {
            case "enable":
                if service.status != .enabled { try service.register() }
            case "disable":
                if service.status != .notRegistered { try service.unregister() }
            case "status":
                break
            case "settings":
                SMAppService.openSystemSettingsLoginItems()
            default:
                throw NSError(domain: "Agenthail", code: 64, userInfo: [NSLocalizedDescriptionKey: "unknown service action: \(action)"])
            }
            FileHandle.standardOutput.write(Data("\(serviceStatusName(service.status))\n".utf8))
            return 0
        } catch {
            FileHandle.standardError.write(Data("\(error.localizedDescription)\n".utf8))
            return 1
        }
    }

    private static func serviceStatusName(_ status: SMAppService.Status) -> String {
        switch status {
        case .notRegistered: return "notRegistered"
        case .enabled: return "enabled"
        case .requiresApproval: return "requiresApproval"
        case .notFound: return "notFound"
        @unknown default: return "unknown"
        }
    }
}

private final class AppDelegate: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        let center = UNUserNotificationCenter.current()
        center.delegate = self
        let open = UNNotificationAction(identifier: openDashboardAction, title: "Open Agenthail")
        center.setNotificationCategories([UNNotificationCategory(identifier: notificationCategory, actions: [open], intentIdentifiers: [])])
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification) async -> UNNotificationPresentationOptions {
        [.banner, .sound]
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse) async {
        if response.actionIdentifier == UNNotificationDefaultActionIdentifier || response.actionIdentifier == openDashboardAction {
            AgenthailProcess.run(["dashboard"])
        }
    }
}

private enum AgenthailProcess {
    static func run(_ arguments: [String]) {
        let process = Process()
        process.executableURL = executableURL()
        process.arguments = arguments
        process.standardOutput = FileHandle.nullDevice
        process.standardError = FileHandle.nullDevice
        try? process.run()
    }

    static func output(_ arguments: [String]) -> (Int32, String) {
        let process = Process()
        let pipe = Pipe()
        process.executableURL = executableURL()
        process.arguments = arguments
        process.standardOutput = pipe
        process.standardError = pipe
        do {
            try process.run()
            process.waitUntilExit()
            let data = pipe.fileHandleForReading.readDataToEndOfFile()
            return (process.terminationStatus, String(decoding: data, as: UTF8.self))
        } catch {
            return (1, error.localizedDescription)
        }
    }

    private static func executableURL() -> URL {
        if let value = ProcessInfo.processInfo.environment["AGENTHAIL_CLI"], !value.isEmpty {
            return URL(fileURLWithPath: value)
        }
        return Bundle.main.bundleURL.deletingLastPathComponent().appendingPathComponent("agenthail")
    }
}

@MainActor
private final class MenuModel: ObservableObject {
    @Published var daemonRunning = false
    @Published var notificationsEnabled = false
    @Published var notificationsDenied = false
    private var timer: AnyCancellable?

    init() {
        refresh()
        timer = Timer.publish(every: 20, on: .main, in: .common).autoconnect().sink { [weak self] _ in
            self?.refresh()
        }
    }

    var daemonLabel: String { daemonRunning ? "Daemon running" : "Daemon unavailable" }

    func refresh() {
        DispatchQueue.global(qos: .utility).async {
            let daemon = AgenthailProcess.output(["daemon", "status"])
            let notifications = AgenthailProcess.output(["daemon", "notify", "status"])
            DispatchQueue.main.async {
                self.daemonRunning = daemon.0 == 0 && daemon.1.contains("running")
                self.notificationsEnabled = notifications.0 == 0 && notifications.1.contains("enabled")
                self.notificationsDenied = notifications.1.contains("denied")
            }
        }
    }

    func setNotifications(_ enabled: Bool) {
        DispatchQueue.global(qos: .userInitiated).async {
            _ = AgenthailProcess.output(["daemon", "notify", enabled ? "on" : "off"])
            DispatchQueue.main.async { self.refresh() }
        }
    }

    func restartDaemon() {
        DispatchQueue.global(qos: .userInitiated).async {
            _ = AgenthailProcess.output(["daemon", "restart"])
            DispatchQueue.main.async { self.refresh() }
        }
    }
}

private struct AgenthailMenuBarApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @StateObject private var model = MenuModel()

    var body: some Scene {
        MenuBarExtra {
            Button(model.daemonLabel) {}
                .disabled(true)
            Button("Open Dashboard") { AgenthailProcess.run(["dashboard"]) }
                .keyboardShortcut("o")
            Divider()
            Toggle("Notifications", isOn: Binding(
                get: { model.notificationsEnabled },
                set: { model.setNotifications($0) }
            ))
            if model.notificationsDenied {
                Button("Open Notification Settings") { _ = NativeCommand.run(["settings"]) }
            }
            Divider()
            Button("Restart Daemon") { model.restartDaemon() }
            Divider()
            Button("Quit Menu Bar App") { NSApplication.shared.terminate(nil) }
                .keyboardShortcut("q")
        } label: {
            Label("Agenthail", systemImage: model.daemonRunning ? "point.3.connected.trianglepath.dotted" : "exclamationmark.triangle")
        }
        .menuBarExtraStyle(.menu)
    }
}

@main
private enum AgenthailMain {
    static func main() {
        let arguments = Array(CommandLine.arguments.dropFirst())
        if !arguments.isEmpty {
            exit(NativeCommand.run(arguments))
        }
        AgenthailMenuBarApp.main()
    }
}
