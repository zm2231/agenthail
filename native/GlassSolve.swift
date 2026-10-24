import CoreGraphics

enum GlassSolve {
    // Measured readable over glass on every built-in background with the built-in roles.
    static let measuredFloor: Float = 0.2

    static func level(polarity: ScrimField.Polarity, window: Float) -> Float {
        let roles = TextColors.builtIn.roles(dark: polarity == .dark, increased: false).map(luminance)
        let boundary = ScrimField.boundary(roles: roles, polarity: polarity, target: 4.5)
        return (boundary - measuredFloor * window) / (1 - measuredFloor)
    }

    static func opacity(under shown: Float?, polarity: ScrimField.Polarity, window: Float, boundary: Float) -> Float {
        let glass = level(polarity: polarity, window: window)
        let worst = shown.map { polarity == .dark ? max($0, glass) : min($0, glass) } ?? glass
        guard !ScrimField.passes(worst, boundary: boundary, polarity: polarity) else { return measuredFloor }
        guard worst != window else { return 1 }
        return min(max((worst - boundary) / (worst - window), measuredFloor), 1)
    }

    static func extreme(_ backdrop: BackdropImage, polarity: ScrimField.Polarity) -> Float? {
        let pw = Int(backdrop.size.width.rounded()), ph = Int(backdrop.size.height.rounded())
        guard pw > 0, ph > 0, let pooled = BackdropRenderer.pool(backdrop.image, to: pw, ph, polarity: polarity) else { return nil }
        return extreme(pooled, polarity: polarity)
    }

    static func extreme(_ pane: BackdropLayers.Pane, tint: BackdropRGB, size: CGSize, polarity: ScrimField.Polarity) -> Float? {
        let pw = Int(size.width.rounded()), ph = Int(size.height.rounded())
        guard pw > 0, ph > 0, let base = pane.base, let pooled = BackdropRenderer.pool(base, to: pw, ph, polarity: polarity) else { return nil }
        let flat = ScrimField.Raster(width: pw, height: ph, fill: (tint.x, tint.y, tint.z))
        let shown = ScrimField.composite(pooled, tint: flat, alpha: [Float](repeating: pane.minimum, count: pw * ph))
        return extreme(shown, polarity: polarity)
    }

    static func extreme(_ raster: ScrimField.Raster, polarity: ScrimField.Polarity) -> Float {
        var worst: Float = polarity == .dark ? 0 : 1
        for i in stride(from: 0, to: raster.rgb.count, by: 3) {
            let l = ScrimField.luminance(raster.rgb[i], raster.rgb[i + 1], raster.rgb[i + 2])
            worst = polarity == .dark ? max(worst, l) : min(worst, l)
        }
        return worst
    }

    static func luminance(_ hex: UInt32) -> Float {
        ScrimField.luminance(Float((hex >> 16) & 0xFF) / 255, Float((hex >> 8) & 0xFF) / 255, Float(hex & 0xFF) / 255)
    }
}
