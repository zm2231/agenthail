@testable import AgenthailHelpers
import AppKit
import SwiftUI

@MainActor
final class ComposerFocusModel: ObservableObject {
    @Published var text = ""
    @Published var status = "idle"
    @Published var focusRequest = 0
    var submits = 0
}

struct ComposerFocusTree: View {
    @ObservedObject var model: ComposerFocusModel

    var body: some View {
        VStack {
            Text(model.status)
            ComposerTextView(text: $model.text, focusRequest: model.focusRequest, placeholder: "Message") { model.submits += 1 }
                .frame(width: 300, height: 80)
        }
    }
}

@main
struct ComposerFocusTest {
    @MainActor
    static func main() {
        _ = NSApplication.shared
        var failed = false
        func check(_ condition: Bool, _ message: String) {
            if !condition {
                failed = true
                print("FAIL: \(message)")
            }
        }

        let model = ComposerFocusModel()
        model.focusRequest = 1
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 400, height: 300), styleMask: [.titled], backing: .buffered, defer: false)
        let container = NSView(frame: window.contentLayoutRect)
        let hosting = NSHostingView(rootView: ComposerFocusTree(model: model))
        hosting.frame = NSRect(x: 0, y: 60, width: 400, height: 240)
        hosting.layoutSubtreeIfNeeded()
        let search = NSTextField(frame: NSRect(x: 10, y: 10, width: 200, height: 24))
        container.addSubview(search)
        window.contentView = container

        func settle() {
            for _ in 0..<3 {
                RunLoop.main.run(until: Date().addingTimeInterval(0.1))
                hosting.layoutSubtreeIfNeeded()
            }
        }
        func holder() -> NSResponder? {
            let responder = window.firstResponder
            if let editor = responder as? NSTextView, editor.isFieldEditor { return editor.delegate as? NSResponder }
            return responder
        }
        func name(_ responder: NSResponder?) -> String {
            responder.map { String(describing: type(of: $0)) } ?? "nothing"
        }
        func press(_ characters: String) {
            let key = NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: [], timestamp: 0, windowNumber: window.windowNumber, context: nil, characters: characters, charactersIgnoringModifiers: characters, isARepeat: false, keyCode: 1)!
            window.sendEvent(key)
            settle()
        }

        container.addSubview(hosting)
        settle()
        check(holder() is ComposerNSTextView, "a request made before the composer has a window focuses it once it has one: \(name(holder()))")

        window.makeFirstResponder(search)
        model.status = "laid out right after the field took focus"
        hosting.layoutSubtreeIfNeeded()
        settle()
        check(holder() === search, "a field that takes focus keeps it through a layout: \(name(holder()))")
        press("s")
        check(search.stringValue == "s" && model.text.isEmpty, "typing goes to the field, not the composer: field \(search.stringValue.debugDescription) composer \(model.text.debugDescription)")

        model.focusRequest += 1
        settle()
        check(holder() is ComposerNSTextView, "a new request moves focus back to the composer: \(name(holder()))")

        window.makeFirstResponder(search)
        model.focusRequest += 1
        hosting.layoutSubtreeIfNeeded()
        settle()
        check(holder() is ComposerNSTextView, "a request made just after the composer resigns still focuses it: \(name(holder()))")
        press("c")
        check(model.text == "c" && search.stringValue == "s", "typing then goes to the composer: composer \(model.text.debugDescription) field \(search.stringValue.debugDescription)")

        exit(failed ? 1 : 0)
    }
}
