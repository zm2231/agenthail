import AppKit
import SwiftUI

// A container's onKeyPress runs before its text fields see the key.
@MainActor
final class TextEditingProbe {
    fileprivate weak var view: NSView?

    var editing: Bool { view?.window?.firstResponder is NSText }
}

struct TextEditingProbeView: NSViewRepresentable {
    let probe: TextEditingProbe

    func makeNSView(context: Context) -> NSView {
        let view = NSView()
        probe.view = view
        return view
    }

    func updateNSView(_ view: NSView, context: Context) {
        probe.view = view
    }
}
