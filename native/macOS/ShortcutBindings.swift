import KeyboardShortcuts
import SwiftUI

extension KeyboardShortcuts.Name {
    static let openPalette = Self("openPalette")
}

extension StoredShortcut {
    init(_ shortcut: KeyboardShortcuts.Shortcut) {
        self.init(carbonKeyCode: shortcut.carbonKeyCode, carbonModifiers: shortcut.carbonModifiers)
    }

    var recorded: KeyboardShortcuts.Shortcut {
        KeyboardShortcuts.Shortcut(carbonKeyCode: carbonKeyCode, carbonModifiers: carbonModifiers)
    }
}

extension ShortcutStore {
    static let shared = ShortcutStore(defaults: .standard, anyApp: AnyAppShortcut(
        current: { KeyboardShortcuts.getShortcut(for: .openPalette).map(StoredShortcut.init) },
        clear: { KeyboardShortcuts.reset(.openPalette) }
    ))

    func keyboardShortcut(_ command: AppCommand) -> KeyboardShortcut? {
        shortcut(for: command)?.recorded.toSwiftUI
    }

    func label(_ command: AppCommand) -> String? {
        shortcut(for: command)?.recorded.description
    }

    func help(_ text: String, _ command: AppCommand) -> String {
        label(command).map { "\(text) \($0)" } ?? text
    }
}

struct KeyboardSettings: View {
    @ObservedObject private var store = ShortcutStore.shared

    private static let groups: [(String, [AppCommand])] = [
        ("Sessions", [.newSession, .goToSession, .findSession, .previousSession, .nextSession, .rename, .openInNewWindow]),
        ("View", [.showRunning, .showRecent, .showAll, .toggleInspector, .jumpToLatest]),
        ("Composer", [.send, .sendAlternate, .stop]),
    ]

    var body: some View {
        Form {
            Section {
                LabeledContent("Open sessions from any app") {
                    KeyboardShortcuts.Recorder(for: .openPalette) { _ in store.anyAppChanged() }
                        .shortcutValidation { shortcut in
                            guard let owner = store.owner(of: StoredShortcut(shortcut), except: .anyApp) else { return .allow }
                            return .disallow(reason: "\(shortcut.description) is already used by \(owner.title).")
                        }
                }
                Text("Brings Agenthail forward with the session palette open, from any app.")
                    .font(.system(size: 12))
                    .foregroundStyle(DesktopPalette.text2)
            }
            ForEach(Self.groups, id: \.0) { group in
                Section(group.0) {
                    ForEach(group.1) { command in
                        row(command)
                    }
                }
            }
            Section {
                HStack {
                    Spacer()
                    Button("Restore Defaults") { store.resetAll() }
                        .disabled(store.overrides.isEmpty && KeyboardShortcuts.getShortcut(for: .openPalette) == nil)
                }
            }
        }
        .formStyle(.grouped)
    }

    private func row(_ command: AppCommand) -> some View {
        LabeledContent(command.title) {
            HStack(spacing: 8) {
                if store.isCustomized(command) {
                    let blocker = store.defaultBlocker(command)
                    Button("Reset") { store.reset(command) }
                        .buttonStyle(.link)
                        .disabled(blocker != nil)
                        .help(blocker.map { "The default is used by \($0.title)." } ?? "Use the default shortcut.")
                }
                KeyboardShortcuts.Recorder(shortcut: Binding(
                    get: { store.shortcut(for: command)?.recorded },
                    set: { store.set($0.map(StoredShortcut.init), for: command) }
                ))
                .shortcutValidation { shortcut in
                    guard let owner = store.owner(of: StoredShortcut(shortcut), except: .command(command)) else { return .allow }
                    return .disallow(reason: "\(shortcut.description) is already used by \(owner.title).")
                }
                .accessibilityLabel(command.title)
            }
        }
    }
}
