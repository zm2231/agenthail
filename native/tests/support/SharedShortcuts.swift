import Foundation

extension ShortcutStore {
    static let shared = ShortcutStore(defaults: UserDefaults(suiteName: "agenthail.helper-tests.shortcuts")!)
}
