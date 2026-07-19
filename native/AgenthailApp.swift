import AppKit
import Combine
import Darwin
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
            writeError("Unable to open macOS notification settings.\n")
            return 1
        case "service":
            return manageService(Array(arguments.dropFirst()))
        default:
            writeError("Unknown Agenthail app command: \(command)\n")
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
            return NotificationState(available: true, authorization: "unknown", authorized: false, alerts: false, sounds: false, error: "Notification settings timed out.")
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
            return printState(NotificationState(available: true, authorization: "unknown", authorized: false, alerts: false, sounds: false, error: "Notification permission request timed out."))
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
            writeError("send requires --title and --message\n")
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
        let request = UNNotificationRequest(identifier: values["identifier"] ?? UUID().uuidString, content: content, trigger: nil)
        let semaphore = DispatchSemaphore(value: 0)
        var sendError: Error?
        center.add(request) { error in
            sendError = error
            semaphore.signal()
        }
        if semaphore.wait(timeout: .now() + 5) == .timedOut {
            writeError("Notification delivery timed out.\n")
            return 1
        }
        if let sendError {
            writeError("\(sendError.localizedDescription)\n")
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
        guard let data = try? JSONEncoder().encode(state) else { return 1 }
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
        let values = [
            "x-apple.systempreferences:com.apple.Notifications-Settings.extension?id=com.agenthail.app",
            "x-apple.systempreferences:com.apple.preference.notifications"
        ]
        return values.contains { value in
            guard let url = URL(string: value) else { return false }
            return NSWorkspace.shared.open(url)
        }
    }

    private static func manageService(_ arguments: [String]) -> Int32 {
        guard let action = arguments.first else {
            writeError("service requires enable, disable, status, or settings\n")
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
                throw NSError(domain: "Agenthail", code: 64, userInfo: [NSLocalizedDescriptionKey: "Unknown service action: \(action)"])
            }
            FileHandle.standardOutput.write(Data("\(serviceStatusName(service.status))\n".utf8))
            return 0
        } catch {
            writeError("\(error.localizedDescription)\n")
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

    private static func writeError(_ value: String) {
        FileHandle.standardError.write(Data(value.utf8))
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
            await MainActor.run {
                let application = NSApplication.shared
                application.activate(ignoringOtherApps: true)
                if let window = application.windows.first(where: { $0.canBecomeMain }) {
                    window.makeKeyAndOrderFront(nil)
                    return
                }
                let configuration = NSWorkspace.OpenConfiguration()
                configuration.activates = true
                NSWorkspace.shared.openApplication(at: Bundle.main.bundleURL, configuration: configuration)
            }
        }
    }
}

enum AgenthailProcess {
    static func run(_ arguments: [String]) {
        let process = configuredProcess(arguments)
        process.standardOutput = FileHandle.nullDevice
        process.standardError = FileHandle.nullDevice
        try? process.run()
    }

    static func output(_ arguments: [String]) -> (Int32, String) {
        let process = configuredProcess(arguments)
        let pipe = Pipe()
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

    private static func configuredProcess(_ arguments: [String]) -> Process {
        let process = Process()
        process.executableURL = executableURL()
        process.arguments = arguments
        var environment = ProcessInfo.processInfo.environment
        let root = "/Library/Application Support/Agenthail"
        if FileManager.default.fileExists(atPath: "\(root)/agenthail") {
            environment["AGENTHAIL_SIDECAR"] = "\(root)/sidecar.py"
            environment["AGENTHAIL_COOKIE_BRIDGE"] = "\(root)/cookie.mjs"
            environment["AGENTHAIL_PYTHON"] = "\(root)/runtime/python/bin/python3"
            environment["AGENTHAIL_MAC_APP"] = Bundle.main.executableURL?.path
            environment["PYTHONPATH"] = "\(root)/pydeps" + environmentSuffix(environment["PYTHONPATH"])
            environment["PATH"] = "\(root)/runtime/node/bin:/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin" + environmentSuffix(environment["PATH"])
            environment["PYTHONDONTWRITEBYTECODE"] = "1"
        }
        process.environment = environment
        return process
    }

    private static func environmentSuffix(_ value: String?) -> String {
        guard let value, !value.isEmpty else { return "" }
        return ":\(value)"
    }

    private static func executableURL() -> URL {
        let environment = ProcessInfo.processInfo.environment
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let candidates = [
            environment["AGENTHAIL_CLI"],
            Bundle.main.resourceURL?.appendingPathComponent("agenthail").path,
            "/opt/homebrew/bin/agenthail",
            "/usr/local/bin/agenthail",
            "\(home)/.local/bin/agenthail"
        ].compactMap { $0 }.filter { !$0.isEmpty }
        if let path = candidates.first(where: { FileManager.default.isExecutableFile(atPath: $0) }) {
            return URL(fileURLWithPath: path)
        }
        return URL(fileURLWithPath: "/usr/bin/false")
    }
}

private enum MenuBarArtwork {
    static let image: NSImage? = {
        let bundle = Bundle.main
        let names = ["AgenthailMenuBarIcon", "AgenthailMenuBarIcon@2x"]
        let image = NSImage(size: NSSize(width: 18, height: 18))
        for name in names {
            guard let url = bundle.url(forResource: name, withExtension: "png"),
                  let data = try? Data(contentsOf: url),
                  let representation = NSBitmapImageRep(data: data) else { continue }
            representation.size = NSSize(width: 18, height: 18)
            image.addRepresentation(representation)
        }
        guard !image.representations.isEmpty else { return nil }
        image.isTemplate = true
        return image
    }()
}

private struct AgenthailMenuContent: View {
    @ObservedObject var model: AgenthailModel
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        Label(model.isConnected ? "Connected" : "Not connected", systemImage: model.isConnected ? "checkmark.circle.fill" : "exclamationmark.triangle.fill")
        Divider()
        Text("Working: \(model.workingSessions.count.formatted())")
        Text("Needs attention: \((model.snapshot?.attention.count ?? 0).formatted())")
        Text("Queued: \((model.snapshot?.queue.count ?? 0).formatted())")
        Divider()
        Button("Open Agenthail") {
            NSApplication.shared.activate(ignoringOtherApps: true)
            openWindow(id: "main")
        }
        .keyboardShortcut("o")
        Button("Restart Agenthail") { model.restartDaemon() }
        Button("Open Login Item Settings") { _ = NativeCommand.run(["service", "settings"]) }
        Divider()
        Button("Quit Agenthail") { NSApplication.shared.terminate(nil) }
            .keyboardShortcut("q")
    }
}

private struct AgenthailMenuBarApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @StateObject private var model = AgenthailModel()

    var body: some Scene {
        WindowGroup("Agenthail", id: "main") {
            AgenthailRootView(model: model)
                .frame(minWidth: 820, minHeight: 560)
        }
        .defaultSize(width: 1180, height: 760)

        MenuBarExtra {
            AgenthailMenuContent(model: model)
        } label: {
            if model.isConnected, let image = MenuBarArtwork.image {
                Image(nsImage: image)
                    .accessibilityLabel("Agenthail")
            } else {
                Image(systemName: "exclamationmark.triangle")
                    .accessibilityLabel("Agenthail unavailable")
            }
        }
        .menuBarExtraStyle(.menu)
    }
}

private final class AgenthailInstanceLock {
    private let descriptor: Int32

    init?() {
        let directory = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".agenthail", isDirectory: true)
        try? FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        let path = directory.appendingPathComponent("menu-app.lock").path
        let descriptor = Darwin.open(path, O_CREAT | O_RDWR, S_IRUSR | S_IWUSR)
        guard descriptor >= 0 else { return nil }
        guard flock(descriptor, LOCK_EX | LOCK_NB) == 0 else {
            Darwin.close(descriptor)
            return nil
        }
        self.descriptor = descriptor
        ftruncate(descriptor, 0)
        let value = "\(getpid())\n"
        value.withCString { pointer in
            _ = Darwin.write(descriptor, pointer, strlen(pointer))
        }
    }

    deinit {
        flock(descriptor, LOCK_UN)
        Darwin.close(descriptor)
    }
}

@main
private enum AgenthailMain {
    static func main() {
        let arguments = Array(CommandLine.arguments.dropFirst())
        if !arguments.isEmpty {
            exit(NativeCommand.run(arguments))
        }
        guard let instanceLock = AgenthailInstanceLock() else {
            NSRunningApplication.runningApplications(withBundleIdentifier: "com.agenthail.app")
                .first { $0.processIdentifier != getpid() }?
                .activate(options: [.activateIgnoringOtherApps])
            return
        }
        withExtendedLifetime(instanceLock) {
            AgenthailMenuBarApp.main()
        }
    }
}
