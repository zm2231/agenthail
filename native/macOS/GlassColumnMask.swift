import AppKit
import SwiftUI

// The split view's column glass has no off switch, so a solid pane masks it to its own shape.
struct GlassColumnMask: NSViewRepresentable {
    let radius: CGFloat

    func makeNSView(context: Context) -> GlassColumnMaskView { GlassColumnMaskView() }

    func updateNSView(_ view: GlassColumnMaskView, context: Context) {
        view.radius = radius
        view.apply()
    }

    static func dismantleNSView(_ view: GlassColumnMaskView, coordinator: ()) {
        view.clear()
    }
}

final class GlassColumnMaskView: NSView {
    var radius: CGFloat = 0
    private weak var glass: NSView?
    private let frames = AncestorFrames()

    override func hitTest(_ point: NSPoint) -> NSView? { nil }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        frames.watch(self) { [weak self] in self?.apply() }
        apply()
    }

    override func layout() {
        super.layout()
        apply()
    }

    override func setFrameOrigin(_ newOrigin: NSPoint) {
        super.setFrameOrigin(newOrigin)
        apply()
    }

    override func setFrameSize(_ newSize: NSSize) {
        super.setFrameSize(newSize)
        apply()
    }

    func apply() {
        guard #available(macOS 26, *), let found = enclosingGlass(), let layer = found.layer else {
            clear()
            return
        }
        if glass !== found { clear() }
        glass = found
        let mask = layer.mask as? CAShapeLayer ?? CAShapeLayer()
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        mask.frame = layer.bounds
        mask.path = CGPath(roundedRect: found.convert(bounds, from: self), cornerWidth: radius, cornerHeight: radius, transform: nil)
        if layer.mask !== mask { layer.mask = mask }
        CATransaction.commit()
    }

    func clear() {
        glass?.layer?.mask = nil
        glass = nil
    }

    @available(macOS 26, *)
    private func enclosingGlass() -> NSGlassEffectView? {
        var current = superview
        while let view = current {
            if let glass = view as? NSGlassEffectView { return glass }
            current = view.superview
        }
        return nil
    }
}
