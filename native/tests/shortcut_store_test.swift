@testable import AgenthailHelpers
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
        let commandG = StoredShortcut(carbonKeyCode: kVK_ANSI_G, carbonModifiers: cmdKey)
        check(store.shortcut(for: .goToSession) == commandK, "a command starts with its default")
        check(store.shortcut(for: .rename) == nil, "a command without a default starts unassigned")

        check(store.set(commandK, for: .newSession) == .taken(.command(.goToSession)), "a shortcut in use is refused and names its owner")
        check(store.shortcut(for: .newSession) == AppCommand.newSession.defaultShortcut, "a refused change leaves the shortcut alone")

        check(store.set(commandG, for: .goToSession) == .saved, "a free shortcut is saved")
        check(store.isCustomized(.goToSession), "a changed command is customized")
        check(store.set(commandK, for: .newSession) == .saved, "a released default can be taken by another command")
        check(store.reset(.goToSession) == .taken(.command(.newSession)), "reset is refused while another command holds the default")
        check(store.shortcut(for: .goToSession) == commandG, "a refused reset keeps the current shortcut")

        check(store.set(nil, for: .stop) == .saved, "a shortcut can be cleared")
        check(store.shortcut(for: .stop) == nil, "a cleared command has no shortcut")
        check(store.isCustomized(.stop), "a cleared default counts as customized")

        let reloaded = ShortcutStore(defaults: defaults)
        check(reloaded.shortcut(for: .goToSession) == commandG && reloaded.shortcut(for: .newSession) == commandK, "changes persist")
        check(reloaded.shortcut(for: .stop) == nil, "a cleared shortcut stays cleared")

        check(store.set(AppCommand.newSession.defaultShortcut, for: .goToSession) == .saved, "a command can take another command's released default")
        check(store.reset(.newSession) == .taken(.command(.goToSession)), "the original owner cannot reset onto it")

        store.resetAll()
        check(store.overrides.isEmpty && defaults.data(forKey: ShortcutStore.defaultsKey) == nil, "restore defaults removes every change")
        check(AppCommand.allCases.allSatisfy { store.shortcut(for: $0) == $0.defaultShortcut }, "every command is back to its default")

        var anyAppShortcut: StoredShortcut?
        let commandN = AppCommand.newSession.defaultShortcut!
        let withAnyApp = ShortcutStore(defaults: defaults, anyApp: AnyAppShortcut(current: { anyAppShortcut }, clear: { anyAppShortcut = nil }))
        check(withAnyApp.set(commandG, for: .newSession) == .saved, "a command moves off its default")
        anyAppShortcut = commandN
        check(withAnyApp.owner(of: commandN) == .anyApp, "the any-app shortcut owns its key")
        check(withAnyApp.reset(.newSession) == .taken(.anyApp), "reset is refused while the any-app shortcut holds the default")
        check(withAnyApp.set(commandN, for: .stop) == .taken(.anyApp), "a command cannot take the any-app shortcut")
        check(withAnyApp.owner(of: commandG, except: .anyApp) == .command(.newSession), "the any-app recorder sees command shortcuts")
        withAnyApp.resetAll()
        check(anyAppShortcut == nil && withAnyApp.shortcut(for: .newSession) == commandN, "restore defaults also clears the any-app shortcut so no key has two owners")

        sharedApp()
    }

    @MainActor
    static func sharedApp() {
        let suite = "agenthail.shortcut-store-test.shared.\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suite)!
        defer { defaults.removePersistentDomain(forName: suite) }

        let commandEqual = StoredShortcut(carbonKeyCode: kVK_ANSI_Equal, carbonModifiers: cmdKey)
        let commandG = StoredShortcut(carbonKeyCode: kVK_ANSI_G, carbonModifiers: cmdKey)
        let commandJ = StoredShortcut(carbonKeyCode: kVK_ANSI_J, carbonModifiers: cmdKey)
        let commandR = AppCommand.refresh.defaultShortcut!

        let store = ShortcutStore(defaults: defaults)
        check(store.set(commandEqual, for: .refresh) == .taken(.sharedApp("Make text bigger")), "the other app's default ⌘= is refused with its title")
        check(store.set(commandEqual, for: .showRunning) == .taken(.sharedApp("Make text bigger")), "a command only this app has is refused too")
        check(store.shortcut(for: .refresh) == commandR && defaults.data(forKey: ShortcutStore.defaultsKey) == nil, "a refused change saves nothing")
        check(store.set(commandJ, for: .refresh) == .taken(.sharedApp("Next session that needs you")), "a shared command cannot take the other app's fixed keys")
        check(store.set(commandJ, for: .showRunning) == .saved, "a command only this app has can use the other app's fixed keys")
        check(store.owner(of: commandJ, except: .anyApp) == .command(.showRunning), "the any-app recorder names this app's owner first")
        check(store.owner(of: StoredShortcut(carbonKeyCode: kVK_ANSI_B, carbonModifiers: cmdKey | optionKey), except: .anyApp) == .sharedApp("Show or hide the brief"), "the any-app shortcut cannot take the other app's fixed keys")

        defaults.set(Data(#"{"textBigger":{"carbonKeyCode":5,"carbonModifiers":256},"futureCommand":null}"#.utf8), forKey: ShortcutStore.defaultsKey)
        let moved = ShortcutStore(defaults: defaults)
        check(moved.set(commandEqual, for: .refresh) == .saved, "⌘= is free once the other app moves Make text bigger off it")
        check(moved.set(commandG, for: .stop) == .taken(.sharedApp("Make text bigger")), "the other app's saved shortcut is taken")
        defaults.set(Data(#"{"textBigger":{"carbonKeyCode":40,"carbonModifiers":256},"goToSession":null,"futureCommand":null}"#.utf8), forKey: ShortcutStore.defaultsKey)
        let blocked = ShortcutStore(defaults: defaults)
        check(blocked.defaultBlocker(.goToSession) == .sharedApp("Make text bigger"), "the other app holding a default blocks its reset")
        check(blocked.reset(.goToSession) == .taken(.sharedApp("Make text bigger")), "reset is refused with the other app's title")
        check(blocked.anyCustomized, "a cleared command counts as customized")

        blocked.resetAll()
        let kept = defaults.data(forKey: ShortcutStore.defaultsKey).flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] } ?? [:]
        check(Set(kept.keys) == ["textBigger", "futureCommand", "goToSession"], "restore defaults keeps the other app's saved keys and the command they block")
        check(blocked.shortcut(for: .goToSession) == nil, "a blocked command keeps its saved shortcut through restore defaults")

        defaults.set(Data(#"{"textBigger":{"carbonKeyCode":5,"carbonModifiers":256},"refresh":{"carbonKeyCode":24,"carbonModifiers":256}}"#.utf8), forKey: ShortcutStore.defaultsKey)
        let free = ShortcutStore(defaults: defaults)
        free.resetAll()
        check(free.overrides.keys.sorted() == ["textBigger"] && !free.anyCustomized, "restore defaults resets this app's commands when nothing blocks them")

        defaults.set(Data(#"{"refresh":{"carbonKeyCode":24,"carbonModifiers":256}}"#.utf8), forKey: ShortcutStore.defaultsKey)
        let earlier = ShortcutStore(defaults: defaults)
        check(earlier.owner(of: commandEqual) == .command(.refresh), "a saved ⌘= displaces the other app's default, as the other app reads it")
        check(earlier.set(commandEqual, for: .refresh) == .saved, "re-recording a saved ⌘= is not a clash")
        check(earlier.set(commandEqual, for: .stop) == .taken(.command(.refresh)), "the saved ⌘= still has one owner")

        defaults.removeObject(forKey: ShortcutStore.defaultsKey)
        let open = ShortcutStore(defaults: defaults)
        defaults.set(Data(#"{"textSmaller":{"carbonKeyCode":5,"carbonModifiers":256}}"#.utf8), forKey: ShortcutStore.defaultsKey)
        check(open.set(commandG, for: .refresh) == .taken(.sharedApp("Make text smaller")), "a change the other app saved while this one runs is seen before saving")
        check(open.set(commandJ, for: .showAll) == .saved, "an unrelated change saves")
        let afterSave = defaults.data(forKey: ShortcutStore.defaultsKey).flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] } ?? [:]
        check(Set(afterSave.keys) == ["textSmaller", "showAll"], "saving keeps the other app's change made while this one runs")
        defaults.set(Data(#"{"textSmaller":{"carbonKeyCode":5,"carbonModifiers":256},"showAll":{"carbonKeyCode":38,"carbonModifiers":256},"talk":null}"#.utf8), forKey: ShortcutStore.defaultsKey)
        open.resetAll()
        let afterReset = defaults.data(forKey: ShortcutStore.defaultsKey).flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] } ?? [:]
        check(Set(afterReset.keys) == ["textSmaller", "talk"], "restore defaults keeps the other app's latest keys")

        defaults.set(Data(#"{"newSession":{"carbonKeyCode":5,"carbonModifiers":256}}"#.utf8), forKey: ShortcutStore.defaultsKey)
        let deadline = Date().addingTimeInterval(2)
        while open.shortcut(for: .newSession) != commandG && Date() < deadline { RunLoop.main.run(until: Date().addingTimeInterval(0.02)) }
        check(open.shortcut(for: .newSession) == commandG && open.isCustomized(.newSession) && open.anyCustomized, "a change the other app saves shows here without a local edit")
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
