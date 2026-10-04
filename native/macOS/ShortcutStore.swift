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

@MainActor
final class ShortcutStore: ObservableObject {
    enum Change: Equatable {
        case saved
        case taken(AppCommand)
    }

    static let defaultsKey = "keyboardShortcuts"
    static let shared = ShortcutStore(defaults: .standard)

    @Published private(set) var overrides: [String: StoredShortcut?]
    private let defaults: UserDefaults

    init(defaults: UserDefaults) {
        self.defaults = defaults
        overrides = defaults.data(forKey: Self.defaultsKey).flatMap { try? JSONDecoder().decode([String: StoredShortcut?].self, from: $0) } ?? [:]
    }

    func shortcut(for command: AppCommand) -> StoredShortcut? {
        if let chosen = overrides[command.rawValue] { return chosen }
        return command.defaultShortcut
    }

    func isCustomized(_ command: AppCommand) -> Bool {
        overrides[command.rawValue] != nil
    }

    func command(using shortcut: StoredShortcut, except excluded: AppCommand? = nil) -> AppCommand? {
        AppCommand.allCases.first { $0 != excluded && self.shortcut(for: $0) == shortcut }
    }

    @discardableResult
    func set(_ shortcut: StoredShortcut?, for command: AppCommand) -> Change {
        if let shortcut, let other = self.command(using: shortcut, except: command) { return .taken(other) }
        if shortcut == command.defaultShortcut {
            overrides.removeValue(forKey: command.rawValue)
        } else {
            overrides.updateValue(shortcut, forKey: command.rawValue)
        }
        save()
        return .saved
    }

    func defaultBlocker(_ command: AppCommand) -> AppCommand? {
        command.defaultShortcut.flatMap { self.command(using: $0, except: command) }
    }

    @discardableResult
    func reset(_ command: AppCommand) -> Change {
        if let other = defaultBlocker(command) { return .taken(other) }
        overrides.removeValue(forKey: command.rawValue)
        save()
        return .saved
    }

    func resetAll() {
        overrides = [:]
        save()
    }

    private func save() {
        if overrides.isEmpty {
            defaults.removeObject(forKey: Self.defaultsKey)
        } else if let data = try? JSONEncoder().encode(overrides) {
            defaults.set(data, forKey: Self.defaultsKey)
        }
    }
}
