import QuartzCore

@MainActor
final class TranscriptScrimRowLayers {
    static let still: [String: CAAction] = Dictionary(uniqueKeysWithValues: ["contents", "bounds", "position", "frame", "opacity", "hidden", "backgroundColor", "mask", "sublayers"].map { ($0, NSNull()) })

    let content = CALayer()
    private var covers: [CALayer] = []
    private var keyed: [ObjectIdentifier: CALayer] = [:]
    private var spare: [CALayer] = []
    private var scale: CGFloat = 0

    init() {
        content.actions = Self.still
    }

    var isEmpty: Bool { keyed.isEmpty && covers.allSatisfy(\.isHidden) }

    func place(_ mask: TranscriptScrimMask, scale: CGFloat) {
        rescale(scale)
        while covers.count < mask.layers.count { covers.append(make()) }
        for (i, layer) in covers.enumerated() {
            let hidden = i >= mask.layers.count
            if layer.isHidden != hidden { layer.isHidden = hidden }
            if !hidden, layer.frame != mask.layers[i] { layer.frame = mask.layers[i] }
        }
    }

    func placeRow(_ row: ObjectIdentifier, _ frame: CGRect?, scale: CGFloat) {
        rescale(scale)
        guard let frame else {
            if let layer = keyed.removeValue(forKey: row) {
                layer.isHidden = true
                spare.append(layer)
            }
            return
        }
        let layer = keyed[row] ?? spare.popLast() ?? make()
        keyed[row] = layer
        if layer.isHidden { layer.isHidden = false }
        if layer.frame != frame { layer.frame = frame }
    }

    private func make() -> CALayer {
        let layer = CALayer()
        layer.actions = Self.still
        layer.contentsCenter = TranscriptScrimMask.stretch
        layer.contents = TranscriptScrim.image(scale: scale)
        layer.contentsScale = scale
        content.addSublayer(layer)
        return layer
    }

    func rescale(_ scale: CGFloat) {
        guard scale != self.scale else { return }
        self.scale = scale
        let image = TranscriptScrim.image(scale: scale)
        for layer in covers + Array(keyed.values) + spare {
            layer.contents = image
            layer.contentsScale = scale
        }
    }
}
