import CoreGraphics
import CoreImage
import Foundation
import ImageIO

typealias BackdropRGB = SIMD3<Float>

final class WallpaperImage: @unchecked Sendable, Equatable {
    let id: String
    let image: CGImage

    init(id: String, image: CGImage) {
        self.id = id
        self.image = image
    }

    static func == (lhs: WallpaperImage, rhs: WallpaperImage) -> Bool { lhs.id == rhs.id }

    static func decode(_ data: Data, id: String, maxPixels: Int) -> WallpaperImage? {
        guard let source = CGImageSourceCreateWithData(data as CFData, nil) else { return nil }
        let options: [CFString: Any] = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceCreateThumbnailWithTransform: true,
            kCGImageSourceThumbnailMaxPixelSize: maxPixels,
        ]
        guard let image = CGImageSourceCreateThumbnailAtIndex(source, 0, options as CFDictionary) else { return nil }
        return WallpaperImage(id: id, image: image)
    }
}

/// Everything the window's backdrop is made of, in points from the
/// window's top left.
struct BackdropSpec: Equatable, Sendable {
    enum Wallpaper: Equatable, Sendable {
        case desktop
        case image(WallpaperImage)
        case linear(angle: Float, stops: [BackdropStop])
        case mesh(columns: Int, rows: Int, colors: [BackdropRGB])
    }

    struct Pane: Equatable, Sendable {
        var id: String
        var rect: CGRect
        var radius: CGFloat
        var minimum: Float
        var strength: Float
        var sharp = false
    }

    var wallpaper: Wallpaper
    var fit = "cover"
    var position = "center"
    var dim: Float = 0
    var strength: Float = 1
    var texture = "scanlines"
    var textureStrength: Float = 0.35
    var textureSpacing: CGFloat = 3
    var blur: CGFloat = 30
    var paneTint: BackdropRGB
    var scrimTint: ScrimField.Tint = .auto
    var feather: Float = 240
    var target: Float = 4.5
    var polarity: ScrimField.Polarity = .dark
    var ground: BackdropRGB
    var roles: [BackdropRGB]
    var size: CGSize
    var scale: CGFloat
    var panes: [Pane] = []
    var texts: [CGRect] = []
    /// Text inside panes. A pane raises its tint only around these; a pane
    /// with none of them is treated as text throughout.
    var paneTexts: [CGRect] = []
    var solidPanes = false
    /// False when the person turned the readability tint off: panes keep
    /// their minimum tint and open areas get no scrim.
    var readability = true
}

struct BackdropStop: Equatable, Sendable {
    var color: BackdropRGB
    var location: Float
}

struct BackdropImage: @unchecked Sendable {
    let image: CGImage
    let size: CGSize
}

enum BackdropRenderer {
    static let context = CIContext(options: [.cacheIntermediates: false])
    /// Points over which a pane's tint falls from what its text needs to the
    /// pane's minimum. Short, so margins and gaps inside a pane show the
    /// background.
    static let paneFeather: Float = 72
    private static let srgb = CGColorSpace(name: CGColorSpace.sRGB)!

    static func render(_ spec: BackdropSpec) -> BackdropImage? {
        let w = Int((spec.size.width * spec.scale).rounded()), h = Int((spec.size.height * spec.scale).rounded())
        let pw = Int(spec.size.width.rounded()), ph = Int(spec.size.height.rounded())
        guard w > 0, h > 0, pw > 0, ph > 0 else { return nil }
        let full = CGRect(x: 0, y: 0, width: w, height: h)
        let transparent = spec.wallpaper == .desktop

        let wallpaper = transparent ? nil : wallpaperImage(spec, width: w, height: h)
        let sharp = transparent ? CIImage.clear.cropped(to: full) : CIImage(cgImage: base(spec, wallpaper: wallpaper, strength: spec.strength, texture: true, width: w, height: h))
        let paneMask = CIImage(cgImage: mask(spec.panes, scale: spec.scale, width: w, height: h))
        var paneBase = sharp
        if !transparent {
            let glass = spec.panes.filter { !$0.sharp }
            for strength in Set(glass.map(\.strength)).sorted() {
                let plain = CIImage(cgImage: base(spec, wallpaper: wallpaper, strength: strength, texture: false, width: w, height: h))
                let blurred = spec.blur > 0 ? plain.clampedToExtent().applyingGaussianBlur(sigma: Double(spec.blur * spec.scale / 3)).cropped(to: full) : plain
                let these = CIImage(cgImage: mask(glass.filter { $0.strength == strength }, scale: spec.scale, width: w, height: h))
                paneBase = blurred.applyingFilter("CIBlendWithMask", parameters: [kCIInputBackgroundImageKey: paneBase, kCIInputMaskImageKey: these])
            }
        }
        let behind = paneBase.applyingFilter("CIBlendWithMask", parameters: [kCIInputBackgroundImageKey: sharp, kCIInputMaskImageKey: paneMask])

        let analysis: ScrimField.Raster
        if transparent {
            let worst: Float = spec.polarity == .dark ? 1 : 0
            analysis = ScrimField.Raster(width: pw, height: ph, fill: (worst, worst, worst))
        } else {
            guard let behindImage = context.createCGImage(behind, from: full, format: .RGBA8, colorSpace: srgb),
                  let pooled = pool(behindImage, to: pw, ph, polarity: spec.polarity) else { return nil }
            analysis = pooled
        }

        let roles = spec.roles.map { ScrimField.luminance($0.x, $0.y, $0.z) }
        let boundary = ScrimField.boundary(roles: roles, polarity: spec.polarity, target: spec.target)
        let paneTint = ScrimField.reachable((spec.paneTint.x, spec.paneTint.y, spec.paneTint.z), polarity: spec.polarity, boundary: boundary)
        var scrimMode = spec.scrimTint
        if case .color(let r, let g, let b) = scrimMode {
            let moved = ScrimField.reachable((r, g, b), polarity: spec.polarity, boundary: boundary)
            scrimMode = .color(moved.0, moved.1, moved.2)
        }
        let scrimTint = ScrimField.tint(for: analysis, mode: scrimMode, polarity: spec.polarity, boundary: boundary)

        let texts = spec.texts.map { rect(outward: $0) }
        let paneTexts = spec.paneTexts.map { rect(outward: $0) }
        let openNeed = ScrimField.needs(image: analysis, tint: scrimTint, texts: texts, polarity: spec.polarity, boundary: boundary)
        let openAlpha = texts.isEmpty || !spec.readability ? [Float](repeating: 0, count: pw * ph)
            : ScrimField.openOpacity(need: openNeed, width: pw, height: ph, feather: spec.feather, minimum: 0)

        var paneAlpha = [Float](repeating: 0, count: pw * ph)
        for pane in spec.panes {
            let r = rect(outward: pane.rect).clamped(width: pw, height: ph)
            guard r.width > 0, r.height > 0 else { continue }
            var alpha: [Float]
            if spec.solidPanes {
                alpha = [Float](repeating: 1, count: r.width * r.height)
            } else if !spec.readability {
                alpha = [Float](repeating: pane.minimum, count: r.width * r.height)
            } else {
                let crop = crop(analysis, to: r)
                let tint = ScrimField.Raster(width: r.width, height: r.height, fill: paneTint)
                let inside = paneTexts.compactMap { $0.intersection(r)?.offset(dx: -r.x, dy: -r.y) }
                if inside.isEmpty {
                    let need = ScrimField.needs(image: crop, tint: tint, texts: [ScrimField.Rect(x: 0, y: 0, width: r.width, height: r.height)], polarity: spec.polarity, boundary: boundary)
                    alpha = ScrimField.paneOpacity(need: need, width: r.width, height: r.height, minimum: pane.minimum)
                } else {
                    let need = ScrimField.needs(image: crop, tint: tint, texts: inside, polarity: spec.polarity, boundary: boundary)
                    alpha = ScrimField.openOpacity(need: need, width: r.width, height: r.height, feather: paneFeather, minimum: pane.minimum)
                }
            }
            for y in 0..<r.height {
                for x in 0..<r.width {
                    let i = (r.y + y) * pw + r.x + x
                    paneAlpha[i] = max(paneAlpha[i], alpha[y * r.width + x])
                }
            }
        }

        let pixels = CGSize(width: w, height: h)
        let up = CGAffineTransform(scaleX: CGFloat(w) / CGFloat(pw), y: CGFloat(h) / CGFloat(ph))
        let paneTintImage = CIImage(color: CIColor(red: CGFloat(paneTint.0), green: CGFloat(paneTint.1), blue: CGFloat(paneTint.2), colorSpace: srgb)!).cropped(to: full)
        let panes = paneTintImage.applyingFilter("CIBlendWithMask", parameters: [
            kCIInputBackgroundImageKey: paneBase,
            kCIInputMaskImageKey: pixelMask(paneAlpha, width: pw, height: ph, pixels: pixels),
        ])
        let open = color(scrimTint).samplingLinear().transformed(by: up).cropped(to: full).applyingFilter("CIBlendWithMask", parameters: [
            kCIInputBackgroundImageKey: sharp,
            kCIInputMaskImageKey: pixelMask(openAlpha, width: pw, height: ph, pixels: pixels),
        ])
        let shown = panes.applyingFilter("CIBlendWithMask", parameters: [kCIInputBackgroundImageKey: open, kCIInputMaskImageKey: paneMask])
        guard let image = context.createCGImage(shown, from: full, format: .RGBA8, colorSpace: srgb) else { return nil }
        return BackdropImage(image: image, size: spec.size)
    }

    private static func bitmap(_ width: Int, _ height: Int, gray: Bool = false) -> CGContext? {
        let context = CGContext(
            data: nil, width: width, height: height, bitsPerComponent: 8, bytesPerRow: 0,
            space: gray ? CGColorSpace(name: CGColorSpace.linearGray)! : srgb,
            bitmapInfo: gray ? CGImageAlphaInfo.none.rawValue : CGImageAlphaInfo.premultipliedLast.rawValue
        )
        context?.translateBy(x: 0, y: CGFloat(height))
        context?.scaleBy(x: 1, y: -1)
        return context
    }

    private static func cgColor(_ c: BackdropRGB, alpha: CGFloat = 1) -> CGColor {
        CGColor(colorSpace: srgb, components: [CGFloat(c.x), CGFloat(c.y), CGFloat(c.z), alpha])!
    }

    private static func wallpaperImage(_ spec: BackdropSpec, width w: Int, height h: Int) -> CGImage? {
        guard let ctx = bitmap(w, h) else { return nil }
        let bounds = CGRect(x: 0, y: 0, width: w, height: h)
        ctx.setFillColor(cgColor(spec.ground))
        ctx.fill(bounds)
        switch spec.wallpaper {
        case .desktop:
            return nil
        case .linear(let angle, let stops):
            let radians = CGFloat(angle) * .pi / 180
            let direction = CGPoint(x: sin(radians), y: -cos(radians))
            let length = abs(CGFloat(w) * direction.x) + abs(CGFloat(h) * direction.y)
            let center = CGPoint(x: CGFloat(w) / 2, y: CGFloat(h) / 2)
            let start = CGPoint(x: center.x - direction.x * length / 2, y: center.y - direction.y * length / 2)
            let end = CGPoint(x: center.x + direction.x * length / 2, y: center.y + direction.y * length / 2)
            let colors = stops.map { cgColor($0.color) } as CFArray
            let locations = stops.map { CGFloat($0.location) }
            guard let gradient = CGGradient(colorsSpace: srgb, colors: colors, locations: locations) else { return nil }
            ctx.drawLinearGradient(gradient, start: start, end: end, options: [.drawsBeforeStartLocation, .drawsAfterEndLocation])
        case .mesh(let columns, let rows, let colors):
            guard let small = mesh(columns: columns, rows: rows, colors: colors, aspect: CGFloat(w) / CGFloat(h)) else { return nil }
            ctx.interpolationQuality = .high
            drawUpright(small, in: ctx, rect: bounds)
        case .image(let wallpaper):
            draw(wallpaper.image, in: ctx, bounds: bounds, fit: spec.fit, position: spec.position)
        }
        return ctx.makeImage()
    }

    private static func mesh(columns: Int, rows: Int, colors: [BackdropRGB], aspect: CGFloat) -> CGImage? {
        let mw = 192, mh = max(Int(192 / max(aspect, 0.1)), 2)
        guard let ctx = CGContext(data: nil, width: mw, height: mh, bitsPerComponent: 8, bytesPerRow: mw * 4, space: srgb, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue),
              let data = ctx.data else { return nil }
        let pixels = data.bindMemory(to: UInt8.self, capacity: mw * mh * 4)
        func smooth(_ t: Float) -> Float { t * t * (3 - 2 * t) }
        for y in 0..<mh {
            let fy = Float(y) / Float(mh - 1) * Float(rows - 1)
            let r0 = min(Int(fy), rows - 2), ty = smooth(fy - Float(r0))
            for x in 0..<mw {
                let fx = Float(x) / Float(mw - 1) * Float(columns - 1)
                let c0 = min(Int(fx), columns - 2), tx = smooth(fx - Float(c0))
                func at(_ c: Int, _ r: Int) -> BackdropRGB {
                    let v = colors[r * columns + c]
                    return BackdropRGB(ScrimField.linear(v.x), ScrimField.linear(v.y), ScrimField.linear(v.z))
                }
                let top = at(c0, r0) * (1 - tx) + at(c0 + 1, r0) * tx
                let bottom = at(c0, r0 + 1) * (1 - tx) + at(c0 + 1, r0 + 1) * tx
                let v = top * (1 - ty) + bottom * ty
                let i = (y * mw + x) * 4
                pixels[i] = UInt8((ScrimField.gamma(v.x) * 255).rounded())
                pixels[i + 1] = UInt8((ScrimField.gamma(v.y) * 255).rounded())
                pixels[i + 2] = UInt8((ScrimField.gamma(v.z) * 255).rounded())
                pixels[i + 3] = 255
            }
        }
        return ctx.makeImage()
    }

    private static func draw(_ image: CGImage, in ctx: CGContext, bounds: CGRect, fit: String, position: String) {
        let iw = CGFloat(image.width), ih = CGFloat(image.height)
        ctx.interpolationQuality = .high
        if fit == "tile" {
            var y: CGFloat = 0
            while y < bounds.height {
                var x: CGFloat = 0
                while x < bounds.width {
                    drawUpright(image, in: ctx, rect: CGRect(x: x, y: y, width: iw, height: ih))
                    x += iw
                }
                y += ih
            }
            return
        }
        let scale: CGFloat
        switch fit {
        case "contain": scale = min(bounds.width / iw, bounds.height / ih)
        case "center": scale = 1
        default: scale = max(bounds.width / iw, bounds.height / ih)
        }
        let size = CGSize(width: iw * scale, height: ih * scale)
        let ax: CGFloat = position == "left" ? 0 : position == "right" ? 1 : 0.5
        let ay: CGFloat = position == "top" ? 0 : position == "bottom" ? 1 : 0.5
        let origin = CGPoint(x: (bounds.width - size.width) * ax, y: (bounds.height - size.height) * ay)
        drawUpright(image, in: ctx, rect: CGRect(origin: origin, size: size))
    }

    private static func drawUpright(_ image: CGImage, in ctx: CGContext, rect: CGRect) {
        ctx.saveGState()
        ctx.translateBy(x: rect.minX, y: rect.maxY)
        ctx.scaleBy(x: 1, y: -1)
        ctx.draw(image, in: CGRect(origin: .zero, size: rect.size))
        ctx.restoreGState()
    }

    private static func base(_ spec: BackdropSpec, wallpaper: CGImage?, strength: Float, texture: Bool, width w: Int, height h: Int) -> CGImage {
        let ctx = bitmap(w, h)!
        let bounds = CGRect(x: 0, y: 0, width: w, height: h)
        ctx.setFillColor(cgColor(spec.ground))
        ctx.fill(bounds)
        if let wallpaper {
            ctx.setAlpha(CGFloat(strength))
            drawUpright(wallpaper, in: ctx, rect: bounds)
            ctx.setAlpha(1)
        }
        if spec.dim > 0 {
            ctx.setFillColor(cgColor(spec.polarity == .dark ? BackdropRGB(0, 0, 0) : BackdropRGB(1, 1, 1), alpha: CGFloat(spec.dim)))
            ctx.fill(bounds)
        }
        if texture, spec.textureStrength > 0 {
            switch spec.texture {
            case "scanlines":
                ctx.setFillColor(cgColor(BackdropRGB(0, 0, 0), alpha: CGFloat(spec.textureStrength)))
                let step = spec.textureSpacing * spec.scale
                var y: CGFloat = 0
                while y < CGFloat(h) {
                    ctx.fill(CGRect(x: 0, y: y, width: CGFloat(w), height: spec.scale))
                    y += step
                }
            case "grain":
                if let grain = grain() {
                    ctx.setAlpha(CGFloat(spec.textureStrength) * 0.5)
                    ctx.setBlendMode(.overlay)
                    ctx.draw(grain, in: CGRect(x: 0, y: 0, width: 256, height: 256), byTiling: true)
                    ctx.setBlendMode(.normal)
                    ctx.setAlpha(1)
                }
            default:
                break
            }
        }
        return ctx.makeImage()!
    }

    private static func grain() -> CGImage? {
        let size = 256
        guard let ctx = CGContext(data: nil, width: size, height: size, bitsPerComponent: 8, bytesPerRow: size, space: CGColorSpaceCreateDeviceGray(), bitmapInfo: CGImageAlphaInfo.none.rawValue),
              let data = ctx.data else { return nil }
        let pixels = data.bindMemory(to: UInt8.self, capacity: size * size)
        var seed: UInt32 = 0x9E37_79B9
        for i in 0..<(size * size) {
            seed ^= seed << 13; seed ^= seed >> 17; seed ^= seed << 5
            pixels[i] = UInt8(truncatingIfNeeded: seed)
        }
        return ctx.makeImage()
    }

    private static func mask(_ panes: [BackdropSpec.Pane], scale: CGFloat, width w: Int, height h: Int) -> CGImage {
        let ctx = bitmap(w, h, gray: true)!
        ctx.setFillColor(gray: 0, alpha: 1)
        ctx.fill(CGRect(x: 0, y: 0, width: w, height: h))
        ctx.setFillColor(gray: 1, alpha: 1)
        for pane in panes {
            let r = CGRect(x: pane.rect.minX * scale, y: pane.rect.minY * scale, width: pane.rect.width * scale, height: pane.rect.height * scale)
            let radius = min(pane.radius * scale, r.width / 2, r.height / 2)
            ctx.addPath(CGPath(roundedRect: r, cornerWidth: radius, cornerHeight: radius, transform: nil))
            ctx.fillPath()
        }
        return ctx.makeImage()!
    }

    /// Points as rasters, each taking the pixel inside it that is hardest
    /// to read on: the brightest for light text, the darkest for dark.
    private static func pool(_ image: CGImage, to pw: Int, _ ph: Int, polarity: ScrimField.Polarity) -> ScrimField.Raster? {
        let w = image.width, h = image.height
        guard let ctx = CGContext(data: nil, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4, space: srgb, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue),
              let data = ctx.data else { return nil }
        ctx.draw(image, in: CGRect(x: 0, y: 0, width: w, height: h))
        let pixels = data.bindMemory(to: UInt8.self, capacity: w * h * 4)
        let lut = (0..<256).map { ScrimField.linear(Float($0) / 255) }
        var rgb = [Float](repeating: 0, count: pw * ph * 3)
        var best = [Float](repeating: polarity == .dark ? -1 : 2, count: pw * ph)
        for y in 0..<h {
            let py = min(y * ph / h, ph - 1)
            for x in 0..<w {
                let px = min(x * pw / w, pw - 1)
                let i = (y * w + x) * 4
                let l = 0.2126 * lut[Int(pixels[i])] + 0.7152 * lut[Int(pixels[i + 1])] + 0.0722 * lut[Int(pixels[i + 2])]
                let j = py * pw + px
                if polarity == .dark ? l > best[j] : l < best[j] {
                    best[j] = l
                    rgb[j * 3] = Float(pixels[i]) / 255
                    rgb[j * 3 + 1] = Float(pixels[i + 1]) / 255
                    rgb[j * 3 + 2] = Float(pixels[i + 2]) / 255
                }
            }
        }
        return ScrimField.Raster(width: pw, height: ph, rgb: rgb)
    }

    private static func crop(_ raster: ScrimField.Raster, to r: ScrimField.Rect) -> ScrimField.Raster {
        var rgb = [Float](repeating: 0, count: r.width * r.height * 3)
        for y in 0..<r.height {
            let from = ((r.y + y) * raster.width + r.x) * 3
            rgb.replaceSubrange((y * r.width * 3)..<((y + 1) * r.width * 3), with: raster.rgb[from..<(from + r.width * 3)])
        }
        return ScrimField.Raster(width: r.width, height: r.height, rgb: rgb)
    }

    private static func rect(outward r: CGRect) -> ScrimField.Rect {
        let x0 = Int(r.minX.rounded(.down)), y0 = Int(r.minY.rounded(.down))
        let x1 = Int(r.maxX.rounded(.up)), y1 = Int(r.maxY.rounded(.up))
        return ScrimField.Rect(x: x0, y: y0, width: max(x1 - x0, 0), height: max(y1 - y0, 0))
    }

    /// An opacity map scaled to device pixels. A pixel is interpolated from
    /// the points around it, so each point first takes its neighbours'
    /// maximum and no pixel shows less than its own point's floor.
    static func pixelMask(_ values: [Float], width: Int, height: Int, pixels: CGSize) -> CIImage {
        let grown = ScrimField.maxFilter(values, width: width, height: height, radius: 1)
        let up = CGAffineTransform(scaleX: pixels.width / CGFloat(width), y: pixels.height / CGFloat(height))
        return scalar(grown, width: width, height: height).samplingLinear().transformed(by: up).cropped(to: CGRect(origin: .zero, size: pixels))
    }

    /// A one-channel map as an image Core Image reads without colour
    /// management, so opacities stay the numbers the floor computed.
    private static func scalar(_ values: [Float], width: Int, height: Int) -> CIImage {
        var rgba = [Float](repeating: 1, count: width * height * 4)
        for i in 0..<(width * height) {
            let v = values[i]
            rgba[i * 4] = v; rgba[i * 4 + 1] = v; rgba[i * 4 + 2] = v
        }
        return CIImage(bitmapData: rgba.withUnsafeBytes { Data($0) }, bytesPerRow: width * 16, size: CGSize(width: width, height: height), format: .RGBAf, colorSpace: nil)
    }

    private static func color(_ raster: ScrimField.Raster) -> CIImage {
        var rgba = [Float](repeating: 1, count: raster.width * raster.height * 4)
        for i in 0..<(raster.width * raster.height) {
            rgba[i * 4] = raster.rgb[i * 3]; rgba[i * 4 + 1] = raster.rgb[i * 3 + 1]; rgba[i * 4 + 2] = raster.rgb[i * 3 + 2]
        }
        return CIImage(bitmapData: rgba.withUnsafeBytes { Data($0) }, bytesPerRow: raster.width * 16, size: CGSize(width: raster.width, height: raster.height), format: .RGBAf, colorSpace: srgb)
    }
}
