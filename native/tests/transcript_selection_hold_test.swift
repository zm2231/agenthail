@testable import AgenthailHelpers
import AppKit
import SwiftUI

final class SelectableText: NSView {
    override var acceptsFirstResponder: Bool { true }
    @objc var hasSelectedText = false
}

@MainActor
final class HostedRows {
    let hold = TranscriptSelectionHold()
    let text = SelectableText()
    var frames: [String: CGRect] = [:]
}

struct HostedText: NSViewRepresentable {
    let view: SelectableText
    func makeNSView(context: Context) -> SelectableText { view }
    func updateNSView(_ view: SelectableText, context: Context) {}
}

struct HostedTranscript: View {
    let model: HostedRows

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                ForEach(0..<6) { index in
                    VStack(alignment: .leading) {
                        Text("row \(index)")
                        if index == 2 { HostedText(view: model.text).frame(width: 40, height: 12) }
                    }
                    .frame(height: 60)
                    .padding(.top, 12)
                    .onGeometryChange(for: CGRect.self) { $0.frame(in: .named(TranscriptRows.contentSpace)) } action: { model.frames["r\(index)"] = $0 }
                }
            }
            .padding(24)
            .coordinateSpace(.named(TranscriptRows.contentSpace))
            .background { TranscriptSelectionAnchor(hold: model.hold) }
        }
    }
}

@main
struct TranscriptSelectionHoldTest {
    @MainActor
    static func main() {
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 400, height: 300), styleMask: [.titled], backing: .buffered, defer: true)
        let other = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 400, height: 300), styleMask: [.titled], backing: .buffered, defer: true)
        let anchor = TranscriptSelectionAnchor.Anchor(frame: NSRect(x: 0, y: 0, width: 400, height: 300))
        window.contentView?.addSubview(anchor)
        let text = SelectableText(frame: NSRect(x: 0, y: 200, width: 200, height: 50))
        let composer = NSTextView(frame: NSRect(x: 0, y: 0, width: 200, height: 100))
        let otherText = SelectableText(frame: NSRect(x: 0, y: 200, width: 200, height: 50))
        anchor.addSubview(text)
        window.contentView?.addSubview(composer)
        other.contentView?.addSubview(otherText)
        let row = CGRect(x: 300, y: 190, width: 100, height: 80)
        let elsewhere = CGRect(x: 0, y: 0, width: 400, height: 150)
        let hold = TranscriptSelectionHold()

        expect(!hold.held(over: [row], buttons: 0), "a hold with no anchor holds nothing")
        expect(hold.held(over: [], buttons: 1), "a pressed left button holds the tail")
        hold.anchor = anchor
        expect(!hold.held(over: [row], buttons: 2), "a right button alone holds nothing")

        expect(!hold.held(over: [row], buttons: 0), "text with no selection holds nothing")
        text.hasSelectedText = true
        expect(hold.held(over: [row], buttons: 0), "a selection in a row that would move holds the tail")
        expect(!hold.held(over: [elsewhere], buttons: 0), "a selection outside the rows that would move does not hold the tail")
        expect(!hold.held(over: [], buttons: 0), "with no rows to move nothing is held")
        expect(hold.held(over: [.infinite], buttons: 0), "an unbounded region holds for any selection in the transcript")

        composer.string = "draft"
        composer.setSelectedRange(NSRange(location: 0, length: 5))
        expect(window.makeFirstResponder(composer), "the composer takes focus")
        expect(hold.held(over: [row], buttons: 0), "a transcript selection still holds after focus moves to the composer")
        text.hasSelectedText = false
        expect(!hold.held(over: [row, elsewhere], buttons: 0), "a selection in the composer holds nothing")

        otherText.hasSelectedText = true
        expect(other.makeFirstResponder(otherText), "the other window's text takes focus")
        expect(!hold.held(over: [row], buttons: 0), "a selection in another window does not hold this transcript")
        text.hasSelectedText = true
        expect(hold.held(over: [row], buttons: 0), "a selection in this transcript holds while another window has focus")

        hostedRows()
    }

    @MainActor
    static func hostedRows() {
        _ = NSApplication.shared
        let model = HostedRows()
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 600, height: 400), styleMask: [.titled], backing: .buffered, defer: false)
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.contentView = NSHostingView(rootView: HostedTranscript(model: model))
        RunLoop.main.run(until: Date().addingTimeInterval(0.5))
        guard let selected = model.frames["r2"], let neighbour = model.frames["r4"] else { return expect(false, "hosted rows report their frames") }
        expect(model.hold.anchor?.window === window, "the anchor joins the transcript's window")
        expect(!model.hold.held(over: [selected], buttons: 0), "a hosted row with no selection holds nothing")
        model.text.hasSelectedText = true
        expect(model.hold.held(over: [selected], buttons: 0), "a selection inside a row matches that row's frame in the content space, row \(selected)")
        expect(!model.hold.held(over: [neighbour], buttons: 0), "a selection in one row does not hold another row, row \(neighbour)")
    }

    static func expect(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
