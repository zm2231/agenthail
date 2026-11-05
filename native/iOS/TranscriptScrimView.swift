import SwiftUI
import UIKit

final class TranscriptScrimView: UIView, TranscriptScrimSink {
    private let base = CALayer()
    private let tint = CALayer()
    private let flat = CALayer()
    private let scrim = CALayer()
    private let maskRoot = CALayer()
    private let rows = TranscriptScrimRowLayers()
    private let still = TranscriptScrimRowLayers.still
    private var layers: BackdropLayers?
    private var placed: (mask: TranscriptScrimMask, origin: CGPoint?)?

    override init(frame: CGRect) {
        super.init(frame: frame)
        isUserInteractionEnabled = false
        for layer in [base, tint, flat, scrim, maskRoot] { layer.actions = still }
        base.contentsGravity = .resize
        scrim.contentsGravity = .resize
        maskRoot.addSublayer(rows.content)
        layer.addSublayer(base)
        layer.addSublayer(tint)
        layer.addSublayer(flat)
        layer.addSublayer(scrim)
    }

    required init?(coder: NSCoder) { nil }

    func show(_ layers: BackdropLayers?) {
        guard layers?.panes[.content]?.base !== self.layers?.panes[.content]?.base || layers?.panes[.content]?.scrim !== self.layers?.panes[.content]?.scrim else { return }
        if self.layers != nil, layers != nil {
            let fade = CATransition()
            fade.type = .fade
            fade.duration = 0.2
            layer.add(fade, forKey: "crossfade")
        }
        self.layers = layers
        arrange()
    }

    override func layoutSubviews() {
        super.layoutSubviews()
        arrange()
    }

    private func arrange() {
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        defer { CATransaction.commit() }
        let pane = layers?.panes[.content]
        let stale = layers.map { $0.size != bounds.size } ?? true
        for sub in [base, tint, flat, scrim, maskRoot] { sub.frame = bounds }
        base.contents = pane?.base
        let color = layers.map { CGColor(srgbRed: CGFloat($0.paneTint.x), green: CGFloat($0.paneTint.y), blue: CGFloat($0.paneTint.z), alpha: 1) }
        tint.backgroundColor = color
        tint.opacity = pane?.minimum ?? 0
        flat.backgroundColor = color
        flat.opacity = pane?.scrimPeak ?? 0
        flat.isHidden = !stale || (pane?.scrimPeak ?? 0) <= 0
        scrim.contents = pane?.scrim
        scrim.isHidden = stale || pane?.scrim == nil
        if let placed { place(placed.mask, origin: placed.origin) }
    }

    func place(_ mask: TranscriptScrimMask, origin: CGPoint?) {
        placed = (mask, origin)
        guard let origin else {
            scrim.mask = nil
            return
        }
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        defer { CATransaction.commit() }
        let scale = window?.screen.scale ?? traitCollection.displayScale
        rows.place(mask, scale: scale)
        scrim.mask = maskRoot
        move(to: origin)
    }

    func placeRow(_ row: ObjectIdentifier, _ layer: CGRect?) {
        rows.placeRow(row, layer, scale: window?.screen.scale ?? traitCollection.displayScale)
    }

    func flushSoon(_ scrim: TranscriptScrim) {
        scrim.flush()
    }

    func move(to origin: CGPoint) {
        guard let placed else { return }
        self.placed = (placed.mask, origin)
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        defer { CATransaction.commit() }
        let host = convert(CGPoint.zero, to: nil)
        rows.content.frame = CGRect(x: origin.x - host.x, y: origin.y - host.y, width: 0, height: 0)
    }
}

struct TranscriptBackdropLayer: UIViewRepresentable {
    let layers: BackdropLayers?
    let scrim: TranscriptScrim

    func makeUIView(context: Context) -> TranscriptScrimView { TranscriptScrimView() }

    func updateUIView(_ view: TranscriptScrimView, context: Context) {
        view.show(layers)
        scrim.attach(view)
    }
}
