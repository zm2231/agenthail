import AppKit
import SwiftUI

final class BackdropLayerSet {
    let layers: BackdropLayers

    init(_ layers: BackdropLayers) {
        self.layers = layers
    }
}

struct BackdropLayerStyle: Equatable {
    enum Kind: Equatable {
        case surface
        case card
        case plate
    }

    var kind: Kind
    var role: BackdropRole?
    var tint: CGFloat = 0
    var inset: CGFloat = 0
    var radius: CGFloat = 0
    var fade: CGFloat?
    var clipsToAnchor = false
    var hostsAnchor = false
}

final class BackdropLayerView: NSView {
    private(set) var style = BackdropLayerStyle(kind: .surface)
    private var set: BackdropLayerSet?
    private(set) weak var anchor: ScrimAnchor?
    private let frames = AncestorFrames()
    private let clip = CALayer()
    private let clipShape = CAShapeLayer()
    private let box = CALayer()
    private let base = CALayer()
    private let tint = CALayer()
    private let scrim = CALayer()
    private let scrimImage = CALayer()
    private let scrimFlat = CALayer()
    private let scrimMask = CALayer()
    private let fade = CAGradientLayer()

    override init(frame: NSRect) {
        super.init(frame: frame)
        wantsLayer = true
        layerContentsRedrawPolicy = .never
        let still: [String: CAAction] = Dictionary(uniqueKeysWithValues: ["contents", "bounds", "position", "frame", "opacity", "hidden", "backgroundColor", "path", "cornerRadius", "mask", "sublayers", "startPoint", "endPoint"].map { ($0, NSNull()) })
        for layer in [clip, clipShape, box, base, tint, scrim, scrimImage, scrimFlat, scrimMask, fade] { layer.actions = still }
        base.contentsGravity = .resize
        scrimImage.contentsGravity = .resize
        scrimMask.contentsGravity = .resize
        fade.colors = [CGColor(gray: 0, alpha: 0), CGColor(gray: 0, alpha: 1)]
        scrim.addSublayer(scrimImage)
        scrim.addSublayer(scrimFlat)
        box.addSublayer(base)
        box.addSublayer(tint)
        box.addSublayer(scrim)
        clip.addSublayer(box)
        layer?.addSublayer(clip)
    }

    required init?(coder: NSCoder) { nil }

    override func hitTest(_ point: NSPoint) -> NSView? { nil }

    func apply(style: BackdropLayerStyle, set: BackdropLayerSet?, anchor: ScrimAnchor?) {
        if set !== self.set, self.set != nil, set != nil {
            let transition = CATransition()
            transition.type = .fade
            transition.duration = 0.2
            box.add(transition, forKey: "crossfade")
        }
        if self.anchor !== anchor {
            self.anchor?.remove(self)
            anchor?.add(self)
        }
        self.style = style
        self.set = set
        self.anchor = anchor
        if style.hostsAnchor, let anchor {
            anchor.host = self
            anchor.inset = style.inset
            anchor.radius = style.radius
        }
        moved()
    }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        frames.watch(self) { [weak self] in self?.moved() }
        moved()
    }

    override func layout() {
        super.layout()
        moved()
    }

    override func setFrameOrigin(_ newOrigin: NSPoint) {
        super.setFrameOrigin(newOrigin)
        moved()
    }

    override func setFrameSize(_ newSize: NSSize) {
        super.setFrameSize(newSize)
        moved()
    }

    private func moved() {
        place()
        if style.hostsAnchor { anchor?.invalidate() }
    }

    func place() {
        guard let layer, let content = window?.contentView else { return }
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        defer { CATransaction.commit() }
        clip.frame = layer.bounds
        box.frame = layer.bounds.insetBy(dx: style.inset, dy: style.inset)
        box.cornerRadius = style.radius
        box.masksToBounds = true
        let window = convert(content.bounds, from: content).offsetBy(dx: -box.frame.minX, dy: -box.frame.minY)
        let layers = set?.layers
        let pane = style.role.flatMap { layers?.panes[$0] }
        let stale = layers.map { $0.size != content.bounds.size } ?? true

        if style.kind == .plate {
            base.isHidden = true
            tint.isHidden = true
            scrim.frame = box.bounds
            scrim.mask = nil
            scrim.isHidden = stale || pane?.plate == nil
            scrimImage.frame = window
            scrimImage.contents = pane?.plate
            scrimImage.isHidden = false
            scrimFlat.isHidden = true
            box.mask = nil
            clip.mask = nil
            return
        }
        base.isHidden = style.kind == .card
        base.frame = window
        base.contents = style.role == nil ? layers?.base : pane?.base
        tint.isHidden = style.kind == .card || style.tint <= 0
        tint.frame = box.bounds
        tint.backgroundColor = layers.map { Self.color($0.paneTint) }
        tint.opacity = Float(style.tint)

        let peak = style.role == nil ? layers?.scrimPeak ?? 0 : pane?.scrimPeak ?? 0
        scrim.frame = box.bounds
        scrimImage.frame = window
        scrimImage.contents = style.role == nil ? layers?.scrim : pane?.scrim
        scrimImage.isHidden = stale
        scrimFlat.frame = box.bounds
        scrimFlat.isHidden = !stale || peak <= 0
        scrimFlat.backgroundColor = layers.map { Self.color(style.role == nil ? $0.scrimColor : $0.paneTint) }
        scrimFlat.opacity = peak
        placeScrimMask()
        placeFade()
        placeClip()
    }

    private func placeScrimMask() {
        guard style.kind == .surface else {
            scrim.mask = nil
            scrim.isHidden = false
            return
        }
        guard let mask = anchor?.mask else {
            scrim.mask = nil
            scrim.isHidden = true
            return
        }
        scrimMask.contents = mask.image
        scrimMask.frame = convert(mask.rect, from: nil).offsetBy(dx: -box.frame.minX, dy: -box.frame.minY)
        scrim.mask = scrimMask
        scrim.isHidden = false
    }

    private func placeFade() {
        guard let solid = style.fade else {
            box.mask = nil
            return
        }
        fade.frame = box.bounds
        fade.startPoint = CGPoint(x: 0.5, y: 1)
        fade.endPoint = CGPoint(x: 0.5, y: 1 - solid)
        box.mask = fade
    }

    private func placeClip() {
        guard style.clipsToAnchor, let anchor, let area = anchor.area else {
            clip.mask = nil
            return
        }
        clipShape.frame = clip.bounds
        let local = convert(area, from: nil)
        let radius = min(anchor.radius, local.width / 2, local.height / 2)
        clipShape.path = CGPath(roundedRect: local, cornerWidth: radius, cornerHeight: radius, transform: nil)
        clip.mask = clipShape
    }

    private static func color(_ c: BackdropRGB) -> CGColor {
        CGColor(srgbRed: CGFloat(c.x), green: CGFloat(c.y), blue: CGFloat(c.z), alpha: 1)
    }
}

struct BackdropLayer: NSViewRepresentable {
    let style: BackdropLayerStyle
    let set: BackdropLayerSet?
    let anchor: ScrimAnchor?

    func makeNSView(context: Context) -> BackdropLayerView { BackdropLayerView() }

    func updateNSView(_ view: BackdropLayerView, context: Context) {
        view.apply(style: style, set: set, anchor: anchor)
    }
}
