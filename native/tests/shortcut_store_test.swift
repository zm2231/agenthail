import Carbon.HIToolbox
import Foundation

@main
struct ShortcutStoreTest {
    @MainActor
    static func main() {
        let suite = "agenthail.shortcut-store-test.\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suite)!
        defer { defaults.removePersistentDomain(forName: suite) }

        let defaultShortcuts = AppCommand.allCases.compactMap(\.defaultShortcut)
        check(Set(defaultShortcuts).count == defaultShortcuts.count, "no two commands share a default shortcut")

        let store = ShortcutStore(defaults: defaults)
        let commandK = StoredShortcut(carbonKeyCode: kVK_ANSI_K, carbonModifiers: cmdKey)
        let commandJ = StoredShortcut(carbonKeyCode: kVK_ANSI_J, carbonModifiers: cmdKey)
        check(store.shortcut(for: .goToSession) == commandK, "a command starts with its default")
        check(store.shortcut(for: .rename) == nil, "a command without a default starts unassigned")

        check(store.set(commandK, for: .newSession) == .taken(.command(.goToSession)), "a shortcut in use is refused and names its owner")
        check(store.shortcut(for: .newSession) == AppCommand.newSession.defaultShortcut, "a refused change leaves the shortcut alone")

        check(store.set(commandJ, for: .goToSession) == .saved, "a free shortcut is saved")
        check(store.isCustomized(.goToSession), "a changed command is customized")
        check(store.set(commandK, for: .newSession) == .saved, "a released default can be taken by another command")
        check(store.reset(.goToSession) == .taken(.command(.newSession)), "reset is refused while another command holds the default")
        check(store.shortcut(for: .goToSession) == commandJ, "a refused reset keeps the current shortcut")

        check(store.set(nil, for: .stop) == .saved, "a shortcut can be cleared")
        check(store.shortcut(for: .stop) == nil, "a cleared command has no shortcut")
        check(store.isCustomized(.stop), "a cleared default counts as customized")

        let reloaded = ShortcutStore(defaults: defaults)
        check(reloaded.shortcut(for: .goToSession) == commandJ && reloaded.shortcut(for: .newSession) == commandK, "changes persist")
        check(reloaded.shortcut(for: .stop) == nil, "a cleared shortcut stays cleared")

        check(store.set(AppCommand.newSession.defaultShortcut, for: .goToSession) == .saved, "a command can take another command's released default")
        check(store.reset(.newSession) == .taken(.command(.goToSession)), "the original owner cannot reset onto it")

        store.resetAll()
        check(store.overrides.isEmpty && defaults.data(forKey: ShortcutStore.defaultsKey) == nil, "restore defaults removes every change")
        check(AppCommand.allCases.allSatisfy { store.shortcut(for: $0) == $0.defaultShortcut }, "every command is back to its default")

        var anyAppShortcut: StoredShortcut?
        let commandN = AppCommand.newSession.defaultShortcut!
        let withAnyApp = ShortcutStore(defaults: defaults, anyApp: AnyAppShortcut(current: { anyAppShortcut }, clear: { anyAppShortcut = nil }))
        check(withAnyApp.set(commandJ, for: .newSession) == .saved, "a command moves off its default")
        anyAppShortcut = commandN
        check(withAnyApp.owner(of: commandN) == .anyApp, "the any-app shortcut owns its key")
        check(withAnyApp.reset(.newSession) == .taken(.anyApp), "reset is refused while the any-app shortcut holds the default")
        check(withAnyApp.set(commandN, for: .stop) == .taken(.anyApp), "a command cannot take the any-app shortcut")
        check(withAnyApp.owner(of: commandJ, except: .anyApp) == .command(.newSession), "the any-app recorder sees command shortcuts")
        withAnyApp.resetAll()
        check(anyAppShortcut == nil && withAnyApp.shortcut(for: .newSession) == commandN, "restore defaults also clears the any-app shortcut so no key has two owners")
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
