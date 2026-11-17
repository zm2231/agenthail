@testable import AgenthailHelpers
import Foundation

@main
struct PastedTextTest {
    @MainActor
    static func main() {
        let fifteen = (1...15).map { "line \($0)" }.joined(separator: "\n") + "\n\n"
        check(PastedTextStore.isLong(fifteen), "fifteen lines are a long paste")
        check(!PastedTextStore.isLong((1...8).map { "line \($0)" }.joined(separator: "\n")), "eight lines stay text")
        check(PastedTextStore.isLong(String(repeating: "a", count: 601)) && !PastedTextStore.isLong(String(repeating: "a", count: 600)), "past 600 characters is a long paste")
        check(PastedTextStore.lines(fifteen) == 15, "trailing blank lines are not counted: \(PastedTextStore.lines(fifteen))")
        check(PastedTextStore.label(fifteen) == "Pasted text · 15 lines", "the token names its lines")
        check(PastedTextStore.label(String(repeating: "a", count: 700)) == "Pasted text · 700 characters", "a one-line paste names its length")

        let store = PastedTextStore()
        guard let first = store.add(fifteen), let second = store.add("second paste") else { return check(false, "the store issues markers") }
        check(first != second && first.utf16.count == 2 && second.utf16.count == 2, "each paste is its own two-unit marker")
        let draft = "\(first)Would this be useful for us?"
        check(store.expand(draft) == fifteen + "Would this be useful for us?", "the paste goes back where it was, before the typed question")
        check(store.expand("Look: \(second) then \(first)") == "Look: second paste then " + fifteen, "every marker expands in place")
        let id = String(first.unicodeScalars.last!)
        check(store.expand("icon \(id) and \(first)") == "icon \(id) and " + fifteen, "a typed private-use character equal to a marker's id stays as typed")
        check(store.expand("plain") == "plain", "text without markers is unchanged")
        let gone = String(Unicode.Scalar(0xFDEF)!) + id
        check(store.expand("before \(gone)") == "before \(gone)" && !store.containsMarker(gone), "a marker shape the store never issued is the person's own text and goes out as typed")
        let lead = String(Unicode.Scalar(0xFDD0)!)
        guard let dirty = store.add("copied \(lead)\(id) text") else { return check(false, "the store issues a marker") }
        check(store.expand(dirty) == "copied \(lead)\(id) text", "a paste keeps every character it was given")
        check(store.expand("\(dirty)\(first)") == "copied \(lead)\(id) text" + fifteen, "a paste's own text is not expanded again")
        check(store.containsMarker(draft) && !store.containsMarker("icon \(id)"), "only issued markers count as tokens")
        check(PastedTextStore.draftOffset(1, tokens: [0]) == 2 && PastedTextStore.draftOffset(0, tokens: [0]) == 0, "a view offset after a token is past its whole marker")
        check(PastedTextStore.viewOffset(2, tokens: [0]) == 1 && PastedTextStore.viewOffset(1, tokens: [0]) == 1 && PastedTextStore.viewOffset(4, tokens: [0, 3]) == 3 && PastedTextStore.viewOffset(5, tokens: [0, 3]) == 4 && PastedTextStore.viewOffset(6, tokens: [0, 3]) == 4, "draft offsets map back to the view")
        let full = PastedTextStore()
        let oldest = full.add("paste 0")
        var issued = 1
        while full.add("paste \(issued)") != nil { issued += 1 }
        check(issued == 32 * 0x1700, "the store issues every marker once: \(issued)")
        check(full.add("one more") == nil && oldest.flatMap(full.text(for:)) == "paste 0", "a full store refuses a new paste and keeps the old ones")

        guard let shared = PastedTextStore.shared.add(fifteen) else { return check(false, "the shared store issues a marker") }
        let view = ComposerNSTextView.make()
        let drafted = "\(shared)Would this be useful for us?"
        view.load(drafted)
        check(view.draftText == drafted, "the view gives back the draft with its marker: \(view.draftText.debugDescription)")
        check((view.string as NSString).length == (drafted as NSString).length - 1, "the token is one character in the view")
        check(view.draftOffset(1) == 2, "the caret after the token is after its marker in the draft")
        guard let token = view.textStorage?.attribute(.attachment, at: 0, effectiveRange: nil) as? PasteTokenAttachment else { return check(false, "the paste shows as one token") }
        check(PastedTextStore.shared.expand(view.draftText) == fifteen + "Would this be useful for us?", "the sent text has the paste in place")
        view.finishEditing(token, edited: "edited paste")
        check(view.draftText != drafted, "an edit changes the draft, so it is saved")
        check(PastedTextStore.shared.expand(view.draftText) == "edited paste" + "Would this be useful for us?", "the edit is sent: \(PastedTextStore.shared.expand(view.draftText).debugDescription)")
        view.undoManager?.undo()
        check(view.draftText == drafted, "undo brings back the pasted text: \(view.draftText.debugDescription)")
        view.undoManager?.redo()
        check(PastedTextStore.shared.expand(view.draftText) == "edited paste" + "Would this be useful for us?", "redo brings back the edit")
        view.load("")
        view.insertPastedText(fifteen)
        check(PastedTextStore.shared.expand(view.draftText) == fifteen, "a paste goes in as a token")
        view.undoManager?.undo()
        check(view.draftText.isEmpty, "undo takes the paste out: \(view.draftText.debugDescription)")
        view.undoManager?.redo()
        check(PastedTextStore.shared.expand(view.draftText) == fifteen, "redo puts it back")
        print("pasted text: ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
