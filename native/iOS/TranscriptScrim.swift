import SwiftUI
import UIKit

@MainActor
final class TranscriptScrim {
    private weak var view: TranscriptScrimView?
    private(set) var mask = TranscriptScrimMask(rows: [])
    private(set) var origin: CGPoint?

    func attach(_ view: TranscriptScrimView) {
        guard self.view !== view else { return }
        self.view = view
        view.place(mask, origin: origin)
    }

    func report(_ rows: [CGRect]) {
        let next = TranscriptScrimMask(rows: rows)
        guard next != mask else { return }
        mask = next
        view?.place(mask, origin: origin)
    }

    func move(to origin: CGPoint) {
        guard origin != self.origin else { return }
        let first = self.origin == nil
        self.origin = origin
        if first { view?.place(mask, origin: origin) } else { view?.move(to: origin) }
    }
}

final class TranscriptScrimView: UIView {
    private let base = CALayer()
    private let tint = CALayer()
    private let flat = CALayer()
    private let scrim = CALayer()
    private let maskRoot = CALayer()
    private let content = CALayer()
    private var rows: [CALayer] = []
    private let still: [String: CAAction] = Dictionary(uniqueKeysWithValues: ["contents", "bounds", "position", "frame", "opacity", "hidden", "backgroundColor", "mask", "sublayers"].map { ($0, NSNull()) })
    private var layers: BackdropLayers?
    private var placed: (mask: TranscriptScrimMask, origin: CGPoint?)?
    private static var images: [CGFloat: CGImage] = [:]

    override init(frame: CGRect) {
        super.init(frame: frame)
        isUserInteractionEnabled = false
        for layer in [base, tint, flat, scrim, maskRoot, content] { layer.actions = still }
        base.contentsGravity = .resize
        scrim.contentsGravity = .resize
        maskRoot.addSublayer(content)
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
        let image = Self.image(scale: scale)
        while rows.count < mask.layers.count {
            let row = CALayer()
            row.actions = still
            row.contentsCenter = TranscriptScrimMask.stretch
            content.addSublayer(row)
            rows.append(row)
        }
        for (i, row) in rows.enumerated() {
            row.isHidden = i >= mask.layers.count
            guard i < mask.layers.count else { continue }
            row.contents = image
            row.contentsScale = scale
            row.frame = mask.layers[i]
        }
        scrim.mask = maskRoot
        move(to: origin)
    }

    func move(to origin: CGPoint) {
        guard let placed else { return }
        self.placed = (placed.mask, origin)
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        defer { CATransaction.commit() }
        let host = convert(CGPoint.zero, to: nil)
        content.frame = CGRect(x: origin.x - host.x, y: origin.y - host.y, width: 0, height: 0)
    }

    private static func image(scale: CGFloat) -> CGImage? {
        if let image = images[scale] { return image }
        let image = TranscriptScrimMask.image(scale: scale)
        images[scale] = image
        return image
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
