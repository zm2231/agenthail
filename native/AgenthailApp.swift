import AppKit
import Combine
import CoreSpotlight
import Darwin
import KeyboardShortcuts
import ServiceManagement
import SwiftUI
import UserNotifications

private let notificationCategory = "AGENTHAIL_COMPLETION"
private let sessionNotificationCategory = "AGENTHAIL_SESSION"
private let openDashboardAction = "OPEN_DASHBOARD"
private let replyAction = "REPLY"

private func baseNotificationCategories() -> [UNNotificationCategory] {
    let open = UNNotificationAction(identifier: openDashboardAction, title: "Open Agenthail")
    let reply = UNTextInputNotificationAction(identifier: replyAction, title: "Reply", textInputButtonTitle: "Send", textInputPlaceholder: "Message")
    return [
        UNNotificationCategory(identifier: notificationCategory, actions: [open], intentIdentifiers: []),
        UNNotificationCategory(identifier: sessionNotificationCategory, actions: [reply, open], intentIdentifiers: []),
    ]
}

private func registerNotificationCategories(requestID: String? = nil, actions: [RequestNotificationAction] = []) async {
    await RequestCategoryRegistry.shared.register(requestID: requestID, actions: actions, base: baseNotificationCategories())
}

private struct NotificationState: Codable {
    let available: Bool
    let authorization: String
    let authorized: Bool
    let alerts: Bool
    let sounds: Bool
    let error: String?
}

enum NativeCommand {
    static func run(_ arguments: [String]) -> Int32 {
        guard let command = arguments.first else { return 64 }
        switch command {
        case "status":
            return printState(notificationState())
        case "request":
            return requestAuthorization()
        case "send":
            return send(arguments: Array(arguments.dropFirst()))
        case "withdraw":
            return withdraw(arguments: Array(arguments.dropFirst()))
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

    private static func withdraw(arguments: [String]) -> Int32 {
        guard let identifier = parse(arguments)["identifier"], !identifier.isEmpty else {
            writeError("withdraw requires --identifier\n")
            return 64
        }
        let center = UNUserNotificationCenter.current()
        center.removePendingNotificationRequests(withIdentifiers: [identifier])
        center.removeDeliveredNotifications(withIdentifiers: [identifier])
        let flushed = DispatchSemaphore(value: 0)
        center.getDeliveredNotifications { _ in flushed.signal() }
        return waitForSignal(flushed, timeout: 5) ? 0 : 1
    }

    private static func send(arguments: [String]) -> Int32 {
        let values = parse(arguments)
        guard let title = values["title"], let message = values["message"] else {
            writeError("send requires --title and --message\n")
            return 64
        }
        let center = UNUserNotificationCenter.current()
        let requestID = values["request"].flatMap { $0.isEmpty ? nil : $0 }
        let actions = values["actions"].flatMap { try? JSONDecoder().decode([RequestNotificationAction].self, from: Data($0.utf8)) } ?? []
        let answerable = requestID != nil && !actions.isEmpty && values["session"]?.isEmpty == false
        let registered = DispatchSemaphore(value: 0)
        Task {
            await registerNotificationCategories(requestID: answerable ? requestID : nil, actions: actions)
            registered.signal()
        }
        if !waitForSignal(registered, timeout: 5) {
            writeError("Notification categories timed out.\n")
            return 1
        }
        let content = UNMutableNotificationContent()
        content.title = title
        content.body = message
        content.sound = .default
        content.categoryIdentifier = notificationCategory
        if let sessionID = values["session"], !sessionID.isEmpty {
            content.userInfo = ["sessionId": sessionID]
            content.threadIdentifier = sessionID
            content.categoryIdentifier = sessionNotificationCategory
            if answerable, let requestID {
                content.userInfo["requestId"] = requestID
                content.categoryIdentifier = RequestNotifications.categoryIdentifier(requestID: requestID)
            }
        }
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
    private var duplicateTimer: Timer?
    weak var model: AgenthailModel?

    @MainActor
    func application(_ application: NSApplication, continue userActivity: NSUserActivity, restorationHandler: @escaping ([any NSUserActivityRestoring]) -> Void) -> Bool {
        guard userActivity.activityType == CSSearchableItemActionType,
              let sessionID = userActivity.userInfo?[CSSearchableItemActivityIdentifier] as? String else { return false }
        NotificationRoute.shared.open(sessionID: sessionID)
        return true
    }

    @MainActor
    func applicationDockMenu(_ sender: NSApplication) -> NSMenu? {
        guard let model else { return nil }
        let menu = NSMenu()
        for section in SessionMenuSection.build(model) {
            if menu.numberOfItems > 0 { menu.addItem(.separator()) }
            let header = NSMenuItem(title: section.title, action: nil, keyEquivalent: "")
            header.isEnabled = false
            menu.addItem(header)
            for session in section.sessions {
                let item = NSMenuItem(title: SessionMenuSection.label(session, in: model.knownSessions, reason: section.reasons[session.id]), action: #selector(openSession(_:)), keyEquivalent: "")
                item.target = self
                item.representedObject = session.id
                menu.addItem(item)
            }
        }
        return menu
    }

    @MainActor
    @objc private func openSession(_ item: NSMenuItem) {
        guard let sessionID = item.representedObject as? String else { return }
        NotificationRoute.shared.open(sessionID: sessionID)
    }

    func applicationWillFinishLaunching(_ notification: Notification) {
        NSAppleEventManager.shared().setEventHandler(self, andSelector: #selector(handleURLEvent(_:reply:)), forEventClass: AEEventClass(kInternetEventClass), andEventID: AEEventID(kAEGetURL))
    }

    @MainActor
    @objc private func handleURLEvent(_ event: NSAppleEventDescriptor, reply: NSAppleEventDescriptor) {
        guard let text = event.paramDescriptor(forKeyword: AEKeyword(keyDirectObject))?.stringValue,
              let url = URL(string: text),
              let link = AgenthailLink(url: url) else { return }
        NotificationRoute.shared.open(link)
    }

    @MainActor
    @objc func sendToAgenthail(_ pasteboard: NSPasteboard, userData: String?, error: AutoreleasingUnsafeMutablePointer<NSString?>) {
        guard let text = pasteboard.string(forType: .string)?.trimmingCharacters(in: .whitespacesAndNewlines), !text.isEmpty else {
            error.pointee = "There is no text to send." as NSString
            return
        }
        NotificationRoute.shared.share(text)
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApplication.shared.servicesProvider = self
        NSUpdateDynamicServices()
        let center = UNUserNotificationCenter.current()
        center.delegate = self
        Task { await registerNotificationCategories() }
        KeyboardShortcuts.onKeyUp(for: .openPalette) { NotificationRoute.shared.showPalette() }
        NSWorkspace.shared.notificationCenter.addObserver(self, selector: #selector(applicationLaunched(_:)), name: NSWorkspace.didLaunchApplicationNotification, object: nil)
        terminateDuplicateApplications()
        duplicateTimer = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { [weak self] _ in
            self?.terminateDuplicateApplications()
        }
    }

    func applicationWillTerminate(_ notification: Notification) {
        duplicateTimer?.invalidate()
        NSWorkspace.shared.notificationCenter.removeObserver(self)
    }

    @objc private func applicationLaunched(_ notification: Notification) {
        guard let application = notification.userInfo?[NSWorkspace.applicationUserInfoKey] as? NSRunningApplication else { return }
        guard isDuplicateApplication(application) else { return }
        terminateDuplicateApplication(application)
    }

    private func terminateDuplicateApplications() {
        guard let bundleIdentifier = Bundle.main.bundleIdentifier, !bundleIdentifier.isEmpty else { return }
        NSRunningApplication.runningApplications(withBundleIdentifier: bundleIdentifier).forEach(terminateDuplicateApplication)
    }

    private func terminateDuplicateApplication(_ application: NSRunningApplication) {
        guard isDuplicateApplication(application) else { return }
        application.forceTerminate()
    }

    private func isDuplicateApplication(_ application: NSRunningApplication) -> Bool {
        shouldTerminateDuplicateApplication(
            candidateBundleIdentifier: application.bundleIdentifier,
            candidateProcessIdentifier: application.processIdentifier,
            ownBundleIdentifier: Bundle.main.bundleIdentifier,
            ownProcessIdentifier: getpid()
        )
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification) async -> UNNotificationPresentationOptions {
        [.banner, .sound]
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse) async {
        let userInfo = response.notification.request.content.userInfo
        let sessionID = userInfo["sessionId"] as? String
        if let choice = RequestNotifications.choice(forAction: response.actionIdentifier), let sessionID, let requestID = userInfo["requestId"] as? String {
            await RequestAnswer.send(choice: choice, text: (response as? UNTextInputNotificationResponse)?.userText, sessionID: sessionID, requestID: requestID, api: { try AgenthailAPI() })
            return
        }
        if response.actionIdentifier == replyAction, let sessionID, let text = (response as? UNTextInputNotificationResponse)?.userText {
            await MainActor.run { NotificationRoute.shared.reply(text, to: sessionID) }
            return
        }
        guard response.actionIdentifier == UNNotificationDefaultActionIdentifier || response.actionIdentifier == openDashboardAction else { return }
        await MainActor.run { NotificationRoute.shared.open(sessionID: sessionID) }
    }
}

@MainActor
final class NotificationRoute: ObservableObject {
    struct Request: Equatable {
        let id = UUID()
        var sessionID: String?
        var newSession = false
        var sharedText: String?
        var reply: String?
        var palette = false
    }

    static let shared = NotificationRoute()
    @Published private(set) var latest: Request?
    private var handledID: UUID?

    func open(sessionID: String?) {
        latest = Request(sessionID: sessionID)
    }

    func share(_ text: String) {
        latest = Request(sessionID: nil, sharedText: text)
    }

    func showPalette() {
        latest = Request(sessionID: nil, palette: true)
    }

    func reply(_ text: String, to sessionID: String) {
        latest = Request(sessionID: sessionID, reply: text)
    }

    func open(_ link: AgenthailLink) {
        switch link {
        case .open: latest = Request(sessionID: nil)
        case .newSession: latest = Request(sessionID: nil, newSession: true)
        case .session(let reference): latest = Request(sessionID: reference)
        }
    }

    func claim() -> Request? {
        guard let latest, latest.id != handledID else { return nil }
        handledID = latest.id
        return latest
    }
}

private struct MenuBarLabel: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject private var route = NotificationRoute.shared
    @AppStorage(SpotlightIndex.preferenceKey) private var spotlightSessions = true
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        Group {
            if model.isConnected, let image = MenuBarArtwork.image {
                Image(nsImage: image)
                    .accessibilityLabel("Agenthail")
            } else {
                Image(systemName: "exclamationmark.triangle")
                    .accessibilityLabel("Agenthail unavailable")
            }
        }
        .onAppear(perform: handleRoute)
        .onChange(of: route.latest) { handleRoute() }
        .onChange(of: model.snapshot?.sessions, initial: true) { syncSpotlight() }
        .onChange(of: spotlightSessions) { syncSpotlight() }
        .onChange(of: model.sessionAttention.count, initial: true) { _, count in
            NSApplication.shared.dockTile.badgeLabel = count > 0 ? String(count) : nil
        }
    }

    private func syncSpotlight() {
        guard spotlightSessions else {
            SpotlightIndex.shared.clear()
            return
        }
        if let sessions = model.snapshot?.sessions { SpotlightIndex.shared.update(sessions) }
    }

    private func handleRoute() {
        guard let request = route.claim() else { return }
        if let text = request.reply, let sessionID = request.sessionID {
            Task {
                guard !(await model.reply(text, to: sessionID)) else { return }
                NotificationRoute.shared.open(sessionID: sessionID)
            }
            return
        }
        if let sessionID = request.sessionID { model.openSession(reference: sessionID) }
        NSApplication.shared.activate()
        openWindow(id: "main")
        if request.newSession { model.openNewSession() }
        if request.palette { model.paletteVisible = true }
        if let text = request.sharedText { model.receiveSharedText(text) }
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

private struct SessionMenuSection: Identifiable {
    let title: String
    let sessions: [SessionState]
    var reasons: [String: String] = [:]
    var id: String { title }

    @MainActor
    static func build(_ model: AgenthailModel, limit: Int = 5) -> [SessionMenuSection] {
        let attention = model.sessionAttention
        let needsYou = SessionTree.needsYou(model.knownSessions, attention: attention)
        let others = SessionTree.newestFirst(SessionFamilies.build(model.knownSessions).filter { attention[$0.root.id] == nil }, by: SessionTree.activity)
        return [
            SessionMenuSection(title: "Needs you", sessions: Array(needsYou.prefix(limit)), reasons: attention.mapValues(\.reason)),
            SessionMenuSection(title: "Working", sessions: Array(others.filter(\.isWorking).map(\.root).prefix(limit))),
            SessionMenuSection(title: "Recent", sessions: Array(others.filter { !$0.isWorking }.map(\.root).prefix(limit)))
        ].filter { !$0.sessions.isEmpty }
    }

    static func label(_ session: SessionState, in sessions: [SessionState], reason: String? = nil) -> String {
        if let reason { return "\(SessionFamilies.title(session, in: sessions))  ·  \(reason)" }
        return "\(SessionFamilies.title(session, in: sessions))  ·  \(session.surface.capitalized)\(session.isWorking ? "" : "  ·  " + relativeAge(session.lastActive))"
    }
}

private struct AgenthailMenuContent: View {
    @ObservedObject var model: AgenthailModel
    let voice: VoiceController
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        Label(model.isConnected ? "Connected" : "Agenthail isn't running", systemImage: model.isConnected ? "checkmark.circle.fill" : "exclamationmark.triangle.fill")
        ForEach(SessionMenuSection.build(model)) { section in
            Section(section.title) {
                ForEach(section.sessions) { session in sessionItem(session, reason: section.reasons[session.id]) }
            }
        }
        Divider()
        Button("New Session") {
            open()
            model.openNewSession()
        }
        Button("Open Agenthail") { open() }
            .keyboardShortcut("o")
        Button("Talk to Orchestrator…") {
            voice.route(to: "")
            NSApplication.shared.activate()
            openWindow(id: "voice")
        }
        SettingsLink { Text("Settings…") }
            .keyboardShortcut(",")
        Divider()
        Button("Restart Agenthail") { model.restartDaemon() }
        Button("Open Login Item Settings") { _ = NativeCommand.run(["service", "settings"]) }
        Divider()
        Button("Quit Agenthail") { NSApplication.shared.terminate(nil) }
            .keyboardShortcut("q")
    }

    private func sessionItem(_ session: SessionState, reason: String?) -> some View {
        Button {
            model.mainPane.select(session.id)
            open()
        } label: {
            Text(SessionMenuSection.label(session, in: model.knownSessions, reason: reason))
        }
    }

    private func open() {
        NSApplication.shared.activate()
        openWindow(id: "main")
    }
}

private struct AgenthailMenuBarApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @StateObject private var model: AgenthailModel
    @StateObject private var voice: VoiceController

    init() {
        let model = AgenthailModel()
        let voice = VoiceController()
        model.onSnapshotRequest = { [weak model, weak voice] request, api in
            guard let model, let voice else { return }
            WindowSnapshot.answer(request, model: model, voice: voice, api: api)
        }
        _model = StateObject(wrappedValue: model)
        _voice = StateObject(wrappedValue: voice)
    }

    var body: some Scene {
        Window("Agenthail", id: "main") {
            MainWindowContent(model: model, voice: voice)
        }
        .defaultSize(width: 1440, height: 900)
        .windowStyle(.hiddenTitleBar)
        .commands { SessionCommands(model: model, voice: voice) }

        WindowGroup("Session", id: "session", for: String.self) { $sessionID in
            if let sessionID {
                SessionWindow(model: model, sessionID: sessionID)
                    .frame(minWidth: 620, minHeight: 480)
                    .environmentObject(voice)
            }
        }
        .defaultSize(width: 900, height: 820)
        .windowStyle(.hiddenTitleBar)

        Window("Voice", id: "voice") {
            VoiceWindow(voice: voice) { model.openSession(reference: $0) }
        }
        .defaultSize(width: 460, height: 620)
        .windowResizability(.contentMinSize)

        Settings {
            DesktopSettings(model: model)
        }

        MenuBarExtra {
            AgenthailMenuContent(model: model, voice: voice)
        } label: {
            MenuBarLabel(model: model)
                .onAppear { appDelegate.model = model }
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
