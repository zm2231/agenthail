@testable import AgenthailHelpers
import AppKit
import Carbon.HIToolbox

@main
struct ComposerTextViewTest {
    @MainActor
    static func main() {
        _ = NSApplication.shared
        let suite = "agenthail.composer-text-view-test.\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suite)!
        defer { defaults.removePersistentDomain(forName: suite) }
        let store = ShortcutStore(defaults: defaults)

        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 400, height: 200), styleMask: [.titled], backing: .buffered, defer: false)
        let textView = ComposerNSTextView.make()
        textView.frame = window.contentView!.bounds
        window.contentView = textView
        window.makeFirstResponder(textView)
        var lookups = 0
        textView.command = { lookups += 1; return ComposerCommand.matching($0, in: store)?.selector }
        var submits = 0
        textView.onSubmit = { submits += 1 }

        func type(_ text: String) {
            textView.string = ""
            textView.undoManager?.removeAllActions()
            textView.insertText(text, replacementRange: textView.selectedRange())
        }
        func press(_ keyCode: Int, _ characters: String, _ modifiers: NSEvent.ModifierFlags = []) {
            let event = NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: modifiers, timestamp: 0, windowNumber: window.windowNumber, context: nil, characters: characters, charactersIgnoringModifiers: characters, isARepeat: false, keyCode: UInt16(keyCode))!
            textView.keyDown(with: event)
        }

        type("hello")
        press(kVK_Return, "\r")
        check(submits == 1 && textView.string == "hello", "Return sends and leaves the text alone: \(submits) \(textView.string.debugDescription)")

        type("first line\\")
        press(kVK_Return, "\r")
        check(submits == 1 && textView.string == "first line\n", "Return after a backslash makes a newline instead of sending: \(textView.string.debugDescription)")

        type("abc")
        press(kVK_Return, "\r", .shift)
        check(submits == 1 && textView.string == "abc\n", "⇧Return makes a newline: \(textView.string.debugDescription)")

        type("abc")
        press(kVK_Return, "\r", .option)
        check(submits == 1 && textView.string == "abc\n", "⌥Return makes a newline: \(textView.string.debugDescription)")

        type("abc")
        press(kVK_ANSI_J, "\u{0A}", .control)
        check(textView.string == "abc\n", "⌃J makes a newline: \(textView.string.debugDescription)")

        type("one two")
        press(kVK_ANSI_W, "\u{17}", .control)
        check(textView.string == "one ", "⌃W deletes the previous word: \(textView.string.debugDescription)")

        type("first\nsecond part")
        press(kVK_ANSI_U, "\u{15}", .control)
        check(textView.string == "first\n", "⌃U deletes to the start of the line, not the whole text: \(textView.string.debugDescription)")
        textView.doCommand(by: #selector(NSResponder.yank(_:)))
        check(textView.string == "first\nsecond part", "⌃Y yanks what ⌃U deleted: \(textView.string.debugDescription)")

        let rebound = StoredShortcut(carbonKeyCode: kVK_ANSI_K, carbonModifiers: controlKey | optionKey)
        check(store.set(rebound, for: .composerDeleteWordBackward) == .saved, "a composer shortcut can be rebound")
        type("one two")
        press(kVK_ANSI_W, "\u{17}", .control)
        check(textView.string == "one two", "the old chord stops deleting once rebound: \(textView.string.debugDescription)")
        press(kVK_ANSI_K, "k", [.control, .option])
        check(textView.string == "one ", "the new chord deletes the previous word: \(textView.string.debugDescription)")
        store.resetAll()

        type("draft")
        textView.setMarkedText("か", selectedRange: NSRange(location: 1, length: 0), replacementRange: textView.selectedRange())
        lookups = 0
        press(kVK_ANSI_U, "\u{15}", .control)
        check(lookups == 0 && textView.hasMarkedText(), "while an input method is composing, composer keys go to it")
        textView.unmarkText()

        let undo = textView.undoManager!
        textView.string = ""
        undo.removeAllActions()
        undo.groupsByEvent = false
        undo.beginUndoGrouping()
        textView.insertText("typed", replacementRange: textView.selectedRange())
        undo.endUndoGrouping()
        undo.beginUndoGrouping()
        textView.replaceAll(with: "/model ")
        undo.endUndoGrouping()
        check(textView.string == "/model " && textView.selectedRange() == NSRange(location: 7, length: 0), "a replaced draft puts the caret at its end")
        undo.undo()
        check(textView.string == "typed", "a replaced draft is one undo step: \(textView.string.debugDescription)")

        check(textView.acceptableDragTypes == [.string], "files and images dropped on the composer go to the attachment drop, not into the text")

        let line = ComposerNSTextView.lineHeight
        func height(_ text: String, _ lines: ClosedRange<Int> = 2...10) -> CGFloat {
            textView.string = text
            return textView.fittingHeight(width: 300, lines: lines)
        }
        check(height("one line") == ceil(line * 2), "the composer is at least two lines tall: \(height("one line") / line)")
        check(abs(height("1\n2\n3\n4\n5") - line * 5) <= 1, "the composer grows a line per line: \(height("1\n2\n3\n4\n5") / line)")
        check(abs(height("a\n", 1...10) - line * 2) <= 1, "a trailing newline counts as a line: \(height("a\n", 1...10) / line)")
        check(height(String(repeating: "word ", count: 40), 1...10) > line * 2, "long text wraps and grows at the given width")
        check(height(String(repeating: "x\n", count: 40)) == ceil(line * 10), "past ten lines the composer scrolls instead of growing")

        check(ComposerReturn.continuation(in: "a\\", caret: 2) == NSRange(location: 1, length: 1), "a backslash before the caret is found")
        check(ComposerReturn.continuation(in: "a\\b", caret: 3) == nil, "a backslash elsewhere is ignored")
        check(ComposerReturn.continuation(in: "🙂\\", caret: 3) == NSRange(location: 2, length: 1), "the caret counts UTF-16 units")
        check(ComposerReturn.continuation(in: "", caret: 0) == nil, "an empty draft has no continuation")

        print("composer text view: ok")
    }
}

@MainActor
private func check(_ condition: Bool, _ message: String) {
    if !condition {
        print("FAIL: \(message)")
        exit(1)
    }
}
