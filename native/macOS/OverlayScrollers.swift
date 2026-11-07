import AppKit
import SwiftUI

extension View {
    func overlayScrollers() -> some View {
        background(OverlayScrollersProbe())
    }
}

private struct OverlayScrollersProbe: NSViewRepresentable {
    func makeNSView(context: Context) -> OverlayScrollersView { OverlayScrollersView() }

    func updateNSView(_ view: OverlayScrollersView, context: Context) {
        view.apply()
    }
}

final class OverlayScrollersView: NSView {
    override init(frame: NSRect) {
        super.init(frame: frame)
        // AppKit puts the user's scroller style back whenever the preference changes.
        NotificationCenter.default.addObserver(self, selector: #selector(styleChanged), name: NSScroller.preferredScrollerStyleDidChangeNotification, object: nil)
    }

    required init?(coder: NSCoder) { nil }

    override func hitTest(_ point: NSPoint) -> NSView? { nil }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        apply()
    }

    @objc private func styleChanged() {
        apply()
    }

    func apply() {
        guard let scroll = enclosingScrollView, scroll.scrollerStyle != .overlay else { return }
        scroll.scrollerStyle = .overlay
    }
}
