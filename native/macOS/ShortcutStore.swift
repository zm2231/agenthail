import Carbon.HIToolbox
import Combine
import Foundation

struct StoredShortcut: Codable, Hashable {
    let carbonKeyCode: Int
    let carbonModifiers: Int

    init(carbonKeyCode: Int, carbonModifiers: Int) {
        self.carbonKeyCode = carbonKeyCode
        self.carbonModifiers = carbonModifiers
    }

    fileprivate init(_ keyCode: Int, _ modifiers: Int) {
        self.init(carbonKeyCode: keyCode, carbonModifiers: modifiers)
    }
}

enum AppCommand: String, CaseIterable, Identifiable {
    case newSession
    case goToSession
    case findSession
    case previousSession
    case nextSession
    case showRunning
    case showRecent
    case showAll
    case showArchived
    case toggleSidebar
    case toggleInspector
    case jumpToLatest
    case expandAgentMessages
    case send
    case sendAlternate
    case stop
    case rename
    case openInNewWindow
    case refresh
    case newTab
    case closeTab
    case nextTab
    case previousTab
    case composerNewline
    case composerDeleteToLineStart
    case composerDeleteWordBackward
    case composerPaste
    case composerSendNow
    case composerCycleMode
    case composerInterrupt
    case composerStash
    case composerSearchHistory

    var id: String { rawValue }

    var title: String {
        switch self {
        case .newSession: "New session"
        case .goToSession: "Go to session"
        case .findSession: "Find session"
        case .previousSession: "Previous session"
        case .nextSession: "Next session"
        case .showRunning: "Show Working"
        case .showRecent: "Show Recent"
        case .showAll: "Show All"
        case .showArchived: "Show Archived"
        case .toggleSidebar: "Show or hide sidebar"
        case .toggleInspector: "Show or hide inspector"
        case .jumpToLatest: "Jump to latest"
        case .expandAgentMessages: "Expand agent messages"
        case .send: "Send"
        case .sendAlternate: "Send the other way (queue or steer)"
        case .stop: "Stop"
        case .rename: "Rename session"
        case .openInNewWindow: "Open session in new window"
        case .refresh: "Refresh"
        case .newTab: "New tab"
        case .closeTab: "Close tab"
        case .nextTab: "Next tab"
        case .previousTab: "Previous tab"
        case .composerNewline: "New line"
        case .composerDeleteToLineStart: "Delete to start of line"
        case .composerDeleteWordBackward: "Delete previous word"
        case .composerPaste: "Paste (also ⌘V)"
        case .composerSendNow: "Send now, steering a running turn"
        case .composerCycleMode: "Change mode"
        case .composerInterrupt: "Stop the running turn (twice clears the draft)"
        case .composerStash: "Stash the draft, or bring it back"
        case .composerSearchHistory: "Search what you sent"
        }
    }

    var defaultShortcut: StoredShortcut? {
        switch self {
        case .newSession: StoredShortcut(kVK_ANSI_N, cmdKey)
        case .goToSession: StoredShortcut(kVK_ANSI_K, cmdKey)
        case .findSession: StoredShortcut(kVK_ANSI_F, cmdKey)
        case .previousSession: StoredShortcut(kVK_UpArrow, cmdKey)
        case .nextSession: StoredShortcut(kVK_DownArrow, cmdKey)
        case .showRunning: StoredShortcut(kVK_ANSI_1, cmdKey)
        case .showRecent: StoredShortcut(kVK_ANSI_2, cmdKey)
        case .showAll: StoredShortcut(kVK_ANSI_3, cmdKey)
        case .showArchived: StoredShortcut(kVK_ANSI_4, cmdKey)
        case .toggleSidebar: StoredShortcut(kVK_ANSI_S, cmdKey | controlKey)
        case .toggleInspector: StoredShortcut(kVK_ANSI_I, cmdKey | optionKey)
        case .jumpToLatest: StoredShortcut(kVK_ANSI_L, cmdKey)
        case .expandAgentMessages: StoredShortcut(kVK_ANSI_E, cmdKey | optionKey)
        case .send: StoredShortcut(kVK_Return, cmdKey)
        case .sendAlternate: StoredShortcut(kVK_Return, cmdKey | optionKey)
        case .stop: StoredShortcut(kVK_ANSI_Period, cmdKey)
        case .refresh: StoredShortcut(kVK_ANSI_R, cmdKey)
        case .newTab: StoredShortcut(kVK_ANSI_T, cmdKey)
        case .closeTab: StoredShortcut(kVK_ANSI_W, cmdKey)
        case .nextTab: StoredShortcut(kVK_ANSI_RightBracket, cmdKey | shiftKey)
        case .previousTab: StoredShortcut(kVK_ANSI_LeftBracket, cmdKey | shiftKey)
        case .composerNewline: StoredShortcut(kVK_ANSI_J, controlKey)
        case .composerDeleteToLineStart: StoredShortcut(kVK_ANSI_U, controlKey)
        case .composerDeleteWordBackward: StoredShortcut(kVK_ANSI_W, controlKey)
        case .composerPaste: StoredShortcut(kVK_ANSI_V, controlKey)
        case .composerSendNow: StoredShortcut(kVK_Return, controlKey)
        case .composerCycleMode: StoredShortcut(kVK_Tab, shiftKey)
        case .composerInterrupt: StoredShortcut(kVK_Escape, 0)
        case .composerStash: StoredShortcut(kVK_ANSI_S, controlKey)
        case .composerSearchHistory: StoredShortcut(kVK_ANSI_R, controlKey)
        case .rename, .openInNewWindow: nil
        }
    }
}

enum ShortcutOwner: Equatable {
    case command(AppCommand)
    case anyApp
    case sharedApp(String)

    var title: String {
        switch self {
        case .command(let command): command.title
        case .anyApp: "Open sessions from any app"
        case .sharedApp(let title): title
        }
    }
}

struct AnyAppShortcut {
    let current: () -> StoredShortcut?
    let clear: () -> Void
}

@MainActor
final class ShortcutStore: ObservableObject {
    enum Change: Equatable {
        case saved
        case taken(ShortcutOwner)
    }

    static let defaultsKey = "keyboardShortcuts"

    @Published private(set) var overrides: [String: StoredShortcut?]
    private let defaults: UserDefaults
    private let anyApp: AnyAppShortcut
    private var external: AnyCancellable?

    init(defaults: UserDefaults, anyApp: AnyAppShortcut = AnyAppShortcut(current: { nil }, clear: {})) {
        self.defaults = defaults
        self.anyApp = anyApp
        overrides = [:]
        overrides = stored()
        external = defaults.publisher(for: \.keyboardShortcuts, options: [.new])
            .receive(on: DispatchQueue.main)
            .sink { [weak self] _ in MainActor.assumeIsolated { self?.reload() } }
    }

    func shortcut(for command: AppCommand) -> StoredShortcut? {
        Self.shortcut(for: command, in: overrides)
    }

    func isCustomized(_ command: AppCommand) -> Bool {
        overrides[command.rawValue] != nil
    }

    var anyCustomized: Bool {
        AppCommand.allCases.contains(where: isCustomized)
    }

    func owner(of shortcut: StoredShortcut, except excluded: ShortcutOwner? = nil) -> ShortcutOwner? {
        owner(of: shortcut, except: excluded, in: stored())
    }

    func defaultBlocker(_ command: AppCommand) -> ShortcutOwner? {
        defaultBlocker(command, in: stored())
    }

    @discardableResult
    func set(_ shortcut: StoredShortcut?, for command: AppCommand) -> Change {
        reload()
        if let shortcut, let other = owner(of: shortcut, except: .command(command), in: overrides) { return .taken(other) }
        if shortcut == command.defaultShortcut {
            overrides.removeValue(forKey: command.rawValue)
        } else {
            overrides.updateValue(shortcut, forKey: command.rawValue)
        }
        save()
        return .saved
    }

    @discardableResult
    func reset(_ command: AppCommand) -> Change {
        reload()
        if let other = defaultBlocker(command, in: overrides) { return .taken(other) }
        overrides.removeValue(forKey: command.rawValue)
        save()
        return .saved
    }

    func resetAll() {
        anyApp.clear()
        reload()
        let before = overrides
        var after = before
        for command in AppCommand.allCases { after.removeValue(forKey: command.rawValue) }
        while true {
            let blocked = AppCommand.allCases.filter { after[$0.rawValue] == nil && before[$0.rawValue] != nil && defaultBlocker($0, in: after) != nil }
            if blocked.isEmpty { break }
            for command in blocked { after[command.rawValue] = before[command.rawValue] }
        }
        overrides = after
        save()
    }

    private static func shortcut(for command: AppCommand, in list: [String: StoredShortcut?]) -> StoredShortcut? {
        if let chosen = list[command.rawValue] { return chosen }
        return command.defaultShortcut
    }

    private func sharedShortcut(for command: SharedAppShortcuts.Command, in list: [String: StoredShortcut?]) -> StoredShortcut? {
        if let chosen = list[command.id] { return chosen }
        guard let shortcut = command.defaultShortcut else { return nil }
        let chosenElsewhere = list.contains { $0.key != command.id && $0.value == shortcut }
        return chosenElsewhere || anyApp.current() == shortcut ? nil : shortcut
    }

    private func owner(of shortcut: StoredShortcut, except excluded: ShortcutOwner?, in list: [String: StoredShortcut?]) -> ShortcutOwner? {
        if let command = AppCommand.allCases.first(where: { .command($0) != excluded && Self.shortcut(for: $0, in: list) == shortcut }) {
            return .command(command)
        }
        if excluded != .anyApp && anyApp.current() == shortcut { return .anyApp }
        if let other = SharedAppShortcuts.commands.first(where: { sharedShortcut(for: $0, in: list) == shortcut }) {
            return .sharedApp(other.title)
        }
        if case .command(let command) = excluded, SharedAppShortcuts.thisAppOnly.contains(command) { return nil }
        return SharedAppShortcuts.fixed.first { $0.0 == shortcut }.map { .sharedApp($0.1) }
    }

    private func defaultBlocker(_ command: AppCommand, in list: [String: StoredShortcut?]) -> ShortcutOwner? {
        command.defaultShortcut.flatMap { owner(of: $0, except: .command(command), in: list) }
    }

    private func stored() -> [String: StoredShortcut?] {
        defaults.data(forKey: Self.defaultsKey).flatMap { try? JSONDecoder().decode([String: StoredShortcut?].self, from: $0) } ?? [:]
    }

    private func reload() {
        let current = stored()
        if current != overrides { overrides = current }
    }

    func anyAppChanged() {
        objectWillChange.send()
    }

    private func save() {
        if overrides.isEmpty {
            defaults.removeObject(forKey: Self.defaultsKey)
        } else if let data = try? JSONEncoder().encode(overrides) {
            defaults.set(data, forKey: Self.defaultsKey)
        }
    }
}

private extension UserDefaults {
    @objc dynamic var keyboardShortcuts: Data? { data(forKey: "keyboardShortcuts") }
}
