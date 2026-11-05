import AppKit
import SwiftUI

final class ScrimRowsOriginView: NSView {
    weak var rows: ScrimRows? {
        didSet {
            guard rows !== oldValue else { return }
            if oldValue?.originView === self { oldValue?.originView = nil }
            rows?.originView = self
        }
    }

    private var clip: NSClipView?

    override func hitTest(_ point: NSPoint) -> NSView? { nil }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        let next = window == nil ? nil : enclosingScrollView?.contentView
        guard next !== clip else { return }
        if let clip { NotificationCenter.default.removeObserver(self, name: NSView.boundsDidChangeNotification, object: clip) }
        clip = next
        if let next {
            NotificationCenter.default.addObserver(self, selector: #selector(scrolled), name: NSView.boundsDidChangeNotification, object: next)
        }
        rows?.position()
    }

    override func setFrameOrigin(_ newOrigin: NSPoint) {
        super.setFrameOrigin(newOrigin)
        rows?.position()
    }

    @objc private func scrolled() {
        rows?.position()
    }

    var topLeft: NSPoint { NSPoint(x: 0, y: isFlipped ? 0 : bounds.height) }
}

struct ScrimRowsOrigin: NSViewRepresentable {
    let rows: ScrimRows?

    func makeNSView(context: Context) -> ScrimRowsOriginView { ScrimRowsOriginView() }

    func updateNSView(_ view: ScrimRowsOriginView, context: Context) {
        view.rows = rows
    }

    static func dismantleNSView(_ view: ScrimRowsOriginView, coordinator: ()) {
        view.rows = nil
    }
}

extension View {
    func transcriptScrimOrigin() -> some View {
        modifier(TranscriptScrimOriginModifier())
    }
}

private struct TranscriptScrimOriginModifier: ViewModifier {
    @Environment(\.transcriptScrim) private var scrim
    @Environment(\.backdropPlace) private var place

    func body(content: Content) -> some View {
        if scrim != nil, let anchor = place.anchor {
            content.background { ScrimRowsOrigin(rows: anchor.rows).allowsHitTesting(false).accessibilityHidden(true) }
        } else {
            content
        }
    }
}
