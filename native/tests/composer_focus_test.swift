@testable import AgenthailHelpers
import AppKit
import SwiftUI

@MainActor
final class ComposerFocusModel: ObservableObject {
    @Published var text = ""
    @Published var status = "idle"
    @Published var focused = false
    var submits = 0
}

struct ComposerFocusTree: View {
    @ObservedObject var model: ComposerFocusModel

    var body: some View {
        VStack {
            Text(model.status)
            ComposerTextView(text: $model.text, focused: $model.focused, placeholder: "Message") { model.submits += 1 }
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
        var hosting: NSHostingView<ComposerFocusTree>!
        func settle() {
            for _ in 0..<3 {
                RunLoop.main.run(until: Date().addingTimeInterval(0.1))
                hosting.layoutSubtreeIfNeeded()
            }
        }

        let model = ComposerFocusModel()
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 400, height: 300), styleMask: [.titled], backing: .buffered, defer: false)
        let container = NSView(frame: window.contentLayoutRect)
        hosting = NSHostingView(rootView: ComposerFocusTree(model: model))
        hosting.frame = NSRect(x: 0, y: 60, width: 400, height: 240)
        let search = NSTextField(frame: NSRect(x: 10, y: 10, width: 200, height: 24))
        container.addSubview(hosting)
        container.addSubview(search)
        window.contentView = container
        settle()
        model.focused = true
        settle()

        func holder() -> NSResponder? {
            let responder = window.firstResponder
            if let editor = responder as? NSTextView, editor.isFieldEditor { return editor.delegate as? NSResponder }
            return responder
        }
        check(holder() is ComposerNSTextView, "the composer takes focus when asked: \(holder().map { String(describing: type(of: $0)) } ?? "nothing")")

        window.makeFirstResponder(search)
        model.status = "laid out while the binding still says focused"
        hosting.layoutSubtreeIfNeeded()
        settle()
        check(holder() === search, "a field that takes focus keeps it through a layout: \(holder().map { String(describing: type(of: $0)) } ?? "nothing")")
        check(!model.focused, "the binding follows focus out of the composer")

        let key = NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: [], timestamp: 0, windowNumber: window.windowNumber, context: nil, characters: "s", charactersIgnoringModifiers: "s", isARepeat: false, keyCode: 1)!
        window.sendEvent(key)
        settle()
        check(search.stringValue == "s" && model.text.isEmpty, "typing goes to the field, not the composer: field \(search.stringValue.debugDescription) composer \(model.text.debugDescription)")

        model.focused = true
        settle()
        check(holder() is ComposerNSTextView, "a new request moves focus back to the composer: \(holder().map { String(describing: type(of: $0)) } ?? "nothing")")

        exit(failed ? 1 : 0)
    }
}
