import AppKit
import SwiftUI

@MainActor
final class TranscriptSelectionHold {
    private static let selected = NSSelectorFromString("hasSelectedText")

    weak var anchor: NSView?

    func held(over rows: [CGRect], buttons: Int = NSEvent.pressedMouseButtons) -> Bool {
        if buttons & 1 != 0 { return true }
        guard let anchor, let root = anchor.window?.contentView else { return false }
        return Self.selections(in: root).contains { view in
            let frame = view.convert(view.bounds, to: anchor)
            return rows.contains { $0.minY < frame.maxY && frame.minY < $0.maxY }
        }
    }

    private static func selections(in view: NSView) -> [NSView] {
        let own = view.responds(to: selected) && view.value(forKey: "hasSelectedText") as? Bool == true ? [view] : []
        return own + view.subviews.flatMap(selections)
    }
}

/// Place as a background of the view that defines the rows' coordinate space.
struct TranscriptSelectionAnchor: NSViewRepresentable {
    let hold: TranscriptSelectionHold

    func makeNSView(context: Context) -> Anchor {
        let view = Anchor()
        hold.anchor = view
        return view
    }

    func updateNSView(_ view: Anchor, context: Context) {
        hold.anchor = view
    }

    final class Anchor: NSView {
        override var isFlipped: Bool { true }
        override func hitTest(_ point: NSPoint) -> NSView? { nil }
    }
}
