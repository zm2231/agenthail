@testable import AgenthailHelpers
import AppKit
import SwiftUI

struct Sheet: View {
    @State var message = ""

    var body: some View {
        Form {
            Section { Picker("Agent", selection: .constant(1)) { Text("Claude").tag(1) } }
            Section { FirstMessageField(message: $message) }
        }
        .formStyle(.grouped)
        .frame(width: 520, height: 260)
    }
}

@main
struct FirstMessageFieldTest {
    @MainActor
    static func main() {
        _ = NSApplication.shared
        let hosting = NSHostingView(rootView: Sheet())
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 520, height: 260), styleMask: [.titled], backing: .buffered, defer: false)
        window.contentView = hosting
        window.orderFrontRegardless()
        RunLoop.main.run(until: Date().addingTimeInterval(0.5))
        var fields: [NSTextField] = []
        func walk(_ view: NSView) {
            if let field = view as? NSTextField, field.isEditable { fields.append(field) }
            view.subviews.forEach(walk)
        }
        walk(hosting)
        expect(fields.count == 1, "the sheet has one message field, found \(fields.count)")
        guard let field = fields.first else { return }
        let frame = field.convert(field.bounds, to: nil)
        expect(field.alignment == .natural || field.alignment == .left, "the message reads from the leading edge, alignment \(field.alignment.rawValue)")
        expect(frame.minX < 60 && frame.width > 400, "the message spans its row instead of sitting after a label, frame \(frame)")
        expect(field.placeholderString == "First message" || field.placeholderAttributedString?.string == "First message", "the title shows as the placeholder")
        print("first message field tests passed")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
