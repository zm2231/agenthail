import AppKit
import Carbon.HIToolbox

enum ComposerCommand: CaseIterable {
    case newline
    case deleteToLineStart
    case deleteWordBackward
    case paste

    var appCommand: AppCommand {
        switch self {
        case .newline: .composerNewline
        case .deleteToLineStart: .composerDeleteToLineStart
        case .deleteWordBackward: .composerDeleteWordBackward
        case .paste: .composerPaste
        }
    }

    // Text-system actions, so each edit joins the text view's undo and kill ring.
    var selector: Selector {
        switch self {
        case .newline: #selector(NSResponder.insertNewlineIgnoringFieldEditor(_:))
        case .deleteToLineStart: #selector(NSResponder.deleteToBeginningOfParagraph(_:))
        case .deleteWordBackward: #selector(NSResponder.deleteWordBackward(_:))
        case .paste: #selector(NSText.paste(_:))
        }
    }

    @MainActor
    static func matching(_ shortcut: StoredShortcut, in store: ShortcutStore) -> ComposerCommand? {
        allCases.first { store.shortcut(for: $0.appCommand) == shortcut }
    }
}

enum ComposerReturn {
    static func continuation(in text: String, caret: Int) -> NSRange? {
        let utf16 = text.utf16
        guard caret > 0, caret <= utf16.count else { return nil }
        let before = utf16.index(utf16.startIndex, offsetBy: caret - 1)
        return utf16[before] == UInt16(UInt8(ascii: "\\")) ? NSRange(location: caret - 1, length: 1) : nil
    }
}

extension StoredShortcut {
    init(keyCode: UInt16, modifiers: NSEvent.ModifierFlags) {
        let flags = modifiers.intersection(.deviceIndependentFlagsMask)
        var carbon = 0
        if flags.contains(.command) { carbon |= cmdKey }
        if flags.contains(.option) { carbon |= optionKey }
        if flags.contains(.control) { carbon |= controlKey }
        if flags.contains(.shift) { carbon |= shiftKey }
        self.init(carbonKeyCode: Int(keyCode), carbonModifiers: carbon)
    }
}
