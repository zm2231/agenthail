import Carbon.HIToolbox
import Combine
import Foundation

enum GlobalShortcut: String, CaseIterable, Identifiable {
    case off
    case controlOptionCommandA
    case optionShiftSpace
    case controlOptionA

    static let preferenceKey = "globalShortcut"

    var id: String { rawValue }

    var label: String {
        switch self {
        case .off: return "Off"
        case .controlOptionCommandA: return "⌃⌥⌘A"
        case .optionShiftSpace: return "⌥⇧Space"
        case .controlOptionA: return "⌃⌥A"
        }
    }

    fileprivate var key: (code: UInt32, modifiers: UInt32)? {
        switch self {
        case .off: return nil
        case .controlOptionCommandA: return (UInt32(kVK_ANSI_A), UInt32(controlKey | optionKey | cmdKey))
        case .optionShiftSpace: return (UInt32(kVK_Space), UInt32(optionKey | shiftKey))
        case .controlOptionA: return (UInt32(kVK_ANSI_A), UInt32(controlKey | optionKey))
        }
    }
}

@MainActor
final class GlobalShortcutCenter: ObservableObject {
    static let shared = GlobalShortcutCenter()
    @Published private(set) var unavailable: GlobalShortcut?
    private var hotKey: EventHotKeyRef?
    private var handler: EventHandlerRef?
    private var action: (() -> Void)?
    private(set) var registered: GlobalShortcut = .off

    @discardableResult
    func register(_ shortcut: GlobalShortcut, action: @escaping () -> Void) -> Bool {
        self.action = action
        guard shortcut != registered || hotKey == nil else { return true }
        unregister()
        unavailable = nil
        guard let key = shortcut.key else { return true }
        installHandler()
        var reference: EventHotKeyRef?
        let identifier = EventHotKeyID(signature: OSType(0x4148_4C54), id: 1)
        guard RegisterEventHotKey(key.code, key.modifiers, identifier, GetApplicationEventTarget(), 0, &reference) == noErr, let reference else {
            unavailable = shortcut
            return false
        }
        hotKey = reference
        registered = shortcut
        return true
    }

    func unregister() {
        if let hotKey { UnregisterEventHotKey(hotKey) }
        hotKey = nil
        registered = .off
    }

    private func installHandler() {
        guard handler == nil else { return }
        var event = EventTypeSpec(eventClass: OSType(kEventClassKeyboard), eventKind: UInt32(kEventHotKeyPressed))
        InstallEventHandler(GetApplicationEventTarget(), { _, _, _ in
            Task { @MainActor in GlobalShortcutCenter.shared.action?() }
            return noErr
        }, 1, &event, nil, &handler)
    }
}
