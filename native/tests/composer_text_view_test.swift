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
        textView.command = { lookups += 1; return ComposerCommand.matching($0, in: store) }
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
        undo.groupsByEvent = true

        let board = NSPasteboard(name: NSPasteboard.Name("agenthail.composer-text-view-test.\(UUID().uuidString)"))
        defer { board.releaseGlobally() }
        textView.pasteboard = board
        var pastedImages: [PastedAttachments] = []
        textView.pasteAttachments = { pastedImages.append($0) }
        let png = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: 2, pixelsHigh: 2, bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!.representation(using: .png, properties: [:])!
        board.clearContents()
        board.setData(png, forType: .png)
        type("look")
        press(kVK_ANSI_V, "\u{16}", .control)
        check(pastedImages.count == 1 && pastedImages[0].attachments.first?.isImage == true && textView.string == "look", "⌃V pastes a copied image as an attachment, not into the text")
        textView.paste(nil)
        check(pastedImages.count == 2, "⌘V's paste: pastes a copied image as an attachment too")
        textView.pasteAttachments = nil
        textView.paste(nil)
        check(pastedImages.count == 2, "a session that can't take attachments never gets one from a paste")

        type("look")
        textView.pasteAttachments = { pastedImages.append($0) }
        check(textView.acceptableDragTypes.contains(.fileURL) && textView.acceptableDragTypes.contains(.png), "a session that takes attachments accepts dropped files and images on the text")
        board.clearContents()
        let item = NSPasteboardItem()
        item.setData(png, forType: .png)
        item.setString("https://example.com/shot.png", forType: .string)
        board.writeObjects([item])
        check(textView.dropAttachments(from: board) && pastedImages.count == 3 && textView.string == "look", "a dropped web image attaches instead of inserting its address")
        board.clearContents()
        board.setString("plain words", forType: .string)
        check(!textView.dropAttachments(from: board) && pastedImages.count == 3, "dropped text stays a text drop")
        textView.pasteAttachments = nil
        check(textView.acceptableDragTypes == [.string], "a session that can't take attachments only accepts dropped text")

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
        let composer = ComposerNSTextView.composerLines
        check(height("", composer) == ceil(line), "an empty session composer is one row: \(height("", composer) / line)")
        check(height("one line", composer) == ceil(line), "a single line keeps the session composer one row: \(height("one line", composer) / line)")
        check(abs(height("1\n2\n3", composer) - line * 3) <= 1, "the session composer grows with its text: \(height("1\n2\n3", composer) / line)")
        check(height(String(repeating: "x\n", count: 40), composer) == ceil(line * CGFloat(composer.upperBound)), "the session composer stops growing at its cap and scrolls")

        var commands: [ComposerCommand] = []
        var handles = true
        textView.onCommand = { commands.append($0); return handles }
        type("draft")
        press(kVK_Return, "\r", .control)
        press(kVK_Tab, "\u{19}", .shift)
        press(kVK_Escape, "\u{1B}")
        check(commands == [.sendNow, .cycleMode, .interrupt] && textView.string == "draft", "⌃Return, ⇧Tab and Esc go to the composer's own actions: \(commands)")
        handles = false
        press(kVK_Return, "\r", .control)
        check(textView.string == "draft\n", "⌃Return with nothing to send makes a plain newline, never a line separator: \(textView.string.debugDescription)")
        textView.onCommand = nil

        var ups = 0
        var takesBack = true
        textView.onUpFromFirstLine = { ups += 1; return takesBack }
        type("one\ntwo")
        textView.doCommand(by: #selector(NSResponder.moveUp(_:)))
        check(ups == 0 && textView.selectedRange().location < 7, "↑ on a later line moves the caret up as usual")
        textView.setSelectedRange(NSRange(location: 2, length: 0))
        textView.doCommand(by: #selector(NSResponder.moveUp(_:)))
        check(ups == 1 && textView.selectedRange().location == 2, "↑ on the first line takes queued messages back instead of moving")
        takesBack = false
        textView.doCommand(by: #selector(NSResponder.moveUp(_:)))
        check(ups == 2 && textView.selectedRange().location == 0, "with nothing queued, ↑ on the first line moves to the start as usual")
        textView.onUpFromFirstLine = nil

        var downs = 0
        var stepsForward = true
        textView.onDownFromLastLine = { downs += 1; return stepsForward }
        textView.setSelectedRange(NSRange(location: 1, length: 0))
        textView.doCommand(by: #selector(NSResponder.moveDown(_:)))
        check(downs == 0 && textView.selectedRange().location > 3, "↓ on an earlier line moves the caret down as usual")
        textView.setSelectedRange(NSRange(location: 5, length: 0))
        textView.doCommand(by: #selector(NSResponder.moveDown(_:)))
        check(downs == 1 && textView.selectedRange().location == 5, "↓ on the last line steps forward through sent prompts instead of moving")
        stepsForward = false
        textView.doCommand(by: #selector(NSResponder.moveDown(_:)))
        check(downs == 2 && textView.selectedRange().location == 7, "outside sent prompts, ↓ on the last line moves to the end as usual")
        textView.onDownFromLastLine = nil

        check(ComposerReturn.continuation(in: "a\\", caret: 2) == NSRange(location: 1, length: 1), "a backslash before the caret is found")
        check(ComposerReturn.continuation(in: "a\\b", caret: 3) == nil, "a backslash elsewhere is ignored")
        check(ComposerReturn.continuation(in: "🙂\\", caret: 3) == NSRange(location: 2, length: 1), "the caret counts UTF-16 units")
        check(ComposerReturn.continuation(in: "", caret: 0) == nil, "an empty draft has no continuation")

        outsideTextEndsComposition()
        print("composer text view: ok")
    }

    @MainActor
    static func outsideTextEndsComposition() {
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 400, height: 200), styleMask: [.titled], backing: .buffered, defer: false)
        let textView = ComposerNSTextView.make()
        window.contentView = textView
        window.makeFirstResponder(textView)
        textView.string = "draft "
        textView.setSelectedRange(NSRange(location: 6, length: 0))
        let sync = ComposerTextSync("draft ")
        var bound = "draft "
        let echo = TextEcho { text in sync.viewChanged(to: text) { bound = $0 } }
        textView.delegate = echo

        textView.setMarkedText("か", selectedRange: NSRange(location: 1, length: 0), replacementRange: textView.selectedRange())
        check(textView.hasMarkedText() && sync.textToShow(bound: bound) == nil, "composing changes nothing from outside")
        bound = "draft \n\nshared from another app"
        textView.show(bound: bound, sync: sync)
        check(!textView.hasMarkedText() && textView.string == "draft \n\nshared from another app" && bound == textView.string, "text from outside ends the composition and stays: \(textView.string.debugDescription) \(bound.debugDescription)")
        textView.insertText("!", replacementRange: textView.selectedRange())
        check(bound == "draft \n\nshared from another app!", "typing afterwards continues from it: \(bound.debugDescription)")
        withExtendedLifetime(echo) {}
    }
}

private final class TextEcho: NSObject, NSTextViewDelegate {
    let changed: (String) -> Void
    init(_ changed: @escaping (String) -> Void) { self.changed = changed }
    func textDidChange(_ notification: Notification) {
        guard let textView = notification.object as? NSTextView else { return }
        MainActor.assumeIsolated { changed(textView.string) }
    }
}

@MainActor
private func check(_ condition: Bool, _ message: String) {
    if !condition {
        print("FAIL: \(message)")
        exit(1)
    }
}
