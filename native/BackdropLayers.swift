import CoreGraphics
import CoreImage

enum BackdropRole: Hashable, Sendable {
    case sidebar
    case content
}

struct BackdropLayers: @unchecked Sendable {
    struct Pane {
        let base: CGImage?
        let minimum: Float
        let scrim: CGImage?
        let scrimPeak: Float
    }

    let size: CGSize
    let polarity: ScrimField.Polarity
    let base: CGImage?
    let scrim: CGImage?
    let scrimPeak: Float
    let scrimColor: BackdropRGB
    let paneTint: BackdropRGB
    let panes: [BackdropRole: Pane]
}

extension BackdropRenderer {
    static func layers(_ spec: BackdropSpec, panes styles: [BackdropRole: BackdropSpec.PaneStyle]) -> BackdropLayers? {
        let w = Int((spec.size.width * spec.scale).rounded()), h = Int((spec.size.height * spec.scale).rounded())
        let pw = Int(spec.size.width.rounded()), ph = Int(spec.size.height.rounded())
        guard w > 0, h > 0, pw > 0, ph > 0 else { return nil }
        let transparent = spec.wallpaper == .desktop
        let wallpaper = transparent ? nil : wallpaperImage(spec, width: w, height: h)
        let sharp = transparent ? nil : base(spec, wallpaper: wallpaper, strength: spec.strength, texture: true, width: w, height: h)
        func analysis(_ image: CGImage?) -> ScrimField.Raster? {
            guard let image else {
                let worst: Float = spec.polarity == .dark ? 1 : 0
                return ScrimField.Raster(width: pw, height: ph, fill: (worst, worst, worst))
            }
            return pool(image, to: pw, ph, polarity: spec.polarity)
        }
        guard let open = analysis(sharp) else { return nil }

        let roles = spec.roles.map { ScrimField.luminance($0.x, $0.y, $0.z) }
        let boundary = ScrimField.boundary(roles: roles, polarity: spec.polarity, target: spec.target)
        let paneTint = ScrimField.reachable((spec.paneTint.x, spec.paneTint.y, spec.paneTint.z), polarity: spec.polarity, boundary: boundary)
        var scrimMode = spec.scrimTint
        if case .color(let r, let g, let b) = scrimMode {
            let moved = ScrimField.reachable((r, g, b), polarity: spec.polarity, boundary: boundary)
            scrimMode = .color(moved.0, moved.1, moved.2)
        }
        let scrimTint = ScrimField.tint(for: open, mode: scrimMode, polarity: spec.polarity, boundary: boundary)
        let whole = [ScrimField.Rect(x: 0, y: 0, width: pw, height: ph)]
        func field(_ behind: ScrimField.Raster, _ tint: ScrimField.Raster) -> [Float] {
            let need = ScrimField.needs(image: behind, tint: tint, texts: whole, polarity: spec.polarity, boundary: boundary, layered: true)
            return ScrimField.fieldOpacity(need: need, width: pw, height: ph)
        }

        let scrim = spec.readability ? layerImage(scrimTint, alpha: field(open, scrimTint)) : nil
        let flatTint = ScrimField.Raster(width: pw, height: ph, fill: paneTint)
        var bases: [Float: CGImage] = [:]
        var panes: [BackdropRole: BackdropLayers.Pane] = [:]
        for (role, style) in styles {
            if !transparent, bases[style.strength] == nil {
                bases[style.strength] = paneBase(spec, wallpaper: wallpaper, strength: style.strength, full: CGSize(width: w, height: h), points: CGSize(width: pw, height: ph))
            }
            let behind = transparent ? nil : bases[style.strength]
            var above: (image: CGImage, peak: Float)?
            if spec.readability, !spec.solidPanes, style.minimum < 1, let raster = analysis(behind) {
                let minimum = style.minimum
                above = layerImage(flatTint, alpha: field(raster, flatTint).map { max(($0 - minimum) / (1 - minimum), 0) })
            }
            panes[role] = BackdropLayers.Pane(base: behind, minimum: style.minimum, scrim: above?.image, scrimPeak: above?.peak ?? 0)
        }
        return BackdropLayers(size: spec.size, polarity: spec.polarity, base: sharp, scrim: scrim?.image, scrimPeak: scrim?.peak ?? 0, scrimColor: mean(scrimTint),
                              paneTint: BackdropRGB(paneTint.0, paneTint.1, paneTint.2), panes: panes)
    }

    /// A blur of at least 4 points hides the difference, so blurred bases render at one pixel per point.
    private static func paneBase(_ spec: BackdropSpec, wallpaper: CGImage?, strength: Float, full: CGSize, points: CGSize) -> CGImage? {
        let pixels = spec.blur >= 4 ? points : full
        let bw = Int(pixels.width), bh = Int(pixels.height)
        let plain = CIImage(cgImage: base(spec, wallpaper: wallpaper, strength: strength, texture: false, width: bw, height: bh))
        let bounds = CGRect(x: 0, y: 0, width: bw, height: bh)
        let sigma = Double(spec.blur * pixels.width / spec.size.width / 3)
        let shown = spec.blur > 0 ? plain.clampedToExtent().applyingGaussianBlur(sigma: sigma).cropped(to: bounds) : plain
        return context.createCGImage(shown, from: bounds, format: .RGBA8, colorSpace: srgb)
    }

    private static func layerImage(_ tint: ScrimField.Raster, alpha: [Float]) -> (image: CGImage, peak: Float)? {
        let w = tint.width, h = tint.height
        guard let ctx = CGContext(data: nil, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4, space: srgb, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue),
              let data = ctx.data else { return nil }
        let pixels = data.bindMemory(to: UInt8.self, capacity: w * h * 4)
        var peak: Float = 0
        for i in 0..<(w * h) {
            let a = (min(max(alpha[i], 0), 1) * 255).rounded(.up)
            peak = max(peak, a / 255)
            pixels[i * 4] = UInt8((tint.rgb[i * 3] * a).rounded())
            pixels[i * 4 + 1] = UInt8((tint.rgb[i * 3 + 1] * a).rounded())
            pixels[i * 4 + 2] = UInt8((tint.rgb[i * 3 + 2] * a).rounded())
            pixels[i * 4 + 3] = UInt8(a)
        }
        guard let image = ctx.makeImage() else { return nil }
        return (image, peak)
    }

    private static func mean(_ raster: ScrimField.Raster) -> BackdropRGB {
        var sum = BackdropRGB(0, 0, 0)
        let n = raster.width * raster.height
        for i in 0..<n {
            sum += BackdropRGB(ScrimField.linear(raster.rgb[i * 3]), ScrimField.linear(raster.rgb[i * 3 + 1]), ScrimField.linear(raster.rgb[i * 3 + 2]))
        }
        let m = sum / Float(max(n, 1))
        return BackdropRGB(ScrimField.gamma(m.x), ScrimField.gamma(m.y), ScrimField.gamma(m.z))
    }
}
