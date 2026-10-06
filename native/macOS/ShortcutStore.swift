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
    case toggleInspector
    case jumpToLatest
    case send
    case sendAlternate
    case stop
    case rename
    case openInNewWindow

    var id: String { rawValue }

    var title: String {
        switch self {
        case .newSession: "New session"
        case .goToSession: "Go to session"
        case .findSession: "Find session"
        case .previousSession: "Previous session"
        case .nextSession: "Next session"
        case .showRunning: "Show Running"
        case .showRecent: "Show Recent"
        case .showAll: "Show All"
        case .toggleInspector: "Show or hide inspector"
        case .jumpToLatest: "Jump to latest"
        case .send: "Send"
        case .sendAlternate: "Send the other way (queue or steer)"
        case .stop: "Stop"
        case .rename: "Rename session"
        case .openInNewWindow: "Open session in new window"
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
        case .toggleInspector: StoredShortcut(kVK_ANSI_I, cmdKey | optionKey)
        case .jumpToLatest: StoredShortcut(kVK_ANSI_L, cmdKey)
        case .send: StoredShortcut(kVK_Return, cmdKey)
        case .sendAlternate: StoredShortcut(kVK_Return, cmdKey | optionKey)
        case .stop: StoredShortcut(kVK_ANSI_Period, cmdKey)
        case .rename, .openInNewWindow: nil
        }
    }
}

enum ShortcutOwner: Equatable {
    case command(AppCommand)
    case anyApp

    var title: String {
        switch self {
        case .command(let command): command.title
        case .anyApp: "Open sessions from any app"
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

    init(defaults: UserDefaults, anyApp: AnyAppShortcut = AnyAppShortcut(current: { nil }, clear: {})) {
        self.defaults = defaults
        self.anyApp = anyApp
        overrides = defaults.data(forKey: Self.defaultsKey).flatMap { try? JSONDecoder().decode([String: StoredShortcut?].self, from: $0) } ?? [:]
    }

    func shortcut(for command: AppCommand) -> StoredShortcut? {
        if let chosen = overrides[command.rawValue] { return chosen }
        return command.defaultShortcut
    }

    func isCustomized(_ command: AppCommand) -> Bool {
        overrides[command.rawValue] != nil
    }

    func owner(of shortcut: StoredShortcut, except excluded: ShortcutOwner? = nil) -> ShortcutOwner? {
        if let command = AppCommand.allCases.first(where: { .command($0) != excluded && self.shortcut(for: $0) == shortcut }) {
            return .command(command)
        }
        return excluded != .anyApp && anyApp.current() == shortcut ? .anyApp : nil
    }

    @discardableResult
    func set(_ shortcut: StoredShortcut?, for command: AppCommand) -> Change {
        if let shortcut, let other = owner(of: shortcut, except: .command(command)) { return .taken(other) }
        if shortcut == command.defaultShortcut {
            overrides.removeValue(forKey: command.rawValue)
        } else {
            overrides.updateValue(shortcut, forKey: command.rawValue)
        }
        save()
        return .saved
    }

    func defaultBlocker(_ command: AppCommand) -> ShortcutOwner? {
        command.defaultShortcut.flatMap { owner(of: $0, except: .command(command)) }
    }

    @discardableResult
    func reset(_ command: AppCommand) -> Change {
        if let other = defaultBlocker(command) { return .taken(other) }
        overrides.removeValue(forKey: command.rawValue)
        save()
        return .saved
    }

    func resetAll() {
        anyApp.clear()
        overrides = [:]
        save()
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
