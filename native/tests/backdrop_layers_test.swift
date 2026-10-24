@testable import AgenthailHelpers
import AppKit

@main
struct BackdropLayersTest {
    static func pixels(_ image: CGImage) -> [Float] {
        let w = image.width, h = image.height
        var raw = [UInt8](repeating: 0, count: w * h * 4)
        let ctx = CGContext(data: &raw, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4, space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
        ctx.draw(image, in: CGRect(x: 0, y: 0, width: w, height: h))
        return raw.map { Float($0) / 255 }
    }

    static func noise(_ size: Int) -> WallpaperImage {
        let ctx = CGContext(data: nil, width: size, height: size, bitsPerComponent: 8, bytesPerRow: size * 4, space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
        let data = ctx.data!.bindMemory(to: UInt8.self, capacity: size * size * 4)
        var seed: UInt32 = 0x1234_5678
        for i in 0..<(size * size) {
            seed ^= seed << 13; seed ^= seed >> 17; seed ^= seed << 5
            let v = UInt8(truncatingIfNeeded: seed)
            data[i * 4] = v; data[i * 4 + 1] = UInt8(truncatingIfNeeded: seed >> 8); data[i * 4 + 2] = UInt8(truncatingIfNeeded: seed >> 16); data[i * 4 + 3] = 255
        }
        return WallpaperImage(id: "noise", image: ctx.makeImage()!)
    }

    static func contrast(_ a: Float, _ b: Float) -> Float { (max(a, b) + 0.05) / (min(a, b) + 0.05) }

    static func main() {
        var failures = 0
        let size = CGSize(width: 96, height: 64)
        let wallpaper = noise(96)
        for polarity in [ScrimField.Polarity.dark, .light] {
            for blur: CGFloat in [0, 12] {
                let dark = polarity == .dark
                let ground: BackdropRGB = dark ? BackdropRGB(0.08, 0.08, 0.1) : BackdropRGB(0.95, 0.95, 0.96)
                let roles: [BackdropRGB] = dark ? [BackdropRGB(0.93, 0.93, 0.95), BackdropRGB(0.62, 0.63, 0.66)] : [BackdropRGB(0.08, 0.08, 0.1), BackdropRGB(0.38, 0.39, 0.42)]
                let spec = BackdropSpec(wallpaper: .image(wallpaper), fit: "cover", texture: "none", blur: blur, paneTint: ground, polarity: polarity, ground: ground, roles: roles, size: size, scale: 1)
                guard let layers = BackdropRenderer.layers(spec, panes: [.content: .init(minimum: 0.18, strength: 1)]) else {
                    print("FAIL: no layers for \(polarity) blur \(blur)")
                    failures += 1
                    continue
                }
                let target: Float = 4.5
                let roleL = roles.map { ScrimField.luminance($0.x, $0.y, $0.z) }
                func worst(base: [Float], minimum: Float, tint: BackdropRGB, scrim: [Float], flat: (BackdropRGB, Float)?) -> Float {
                    var worst = Float.infinity
                    for i in 0..<(Int(size.width) * Int(size.height)) {
                        var enc = (base[i * 4], base[i * 4 + 1], base[i * 4 + 2])
                        var lin = (ScrimField.linear(enc.0), ScrimField.linear(enc.1), ScrimField.linear(enc.2))
                        func over(_ c: (Float, Float, Float), _ a: Float) {
                            enc = (enc.0 * (1 - a) + c.0 * a, enc.1 * (1 - a) + c.1 * a, enc.2 * (1 - a) + c.2 * a)
                            lin = (lin.0 * (1 - a) + ScrimField.linear(c.0) * a, lin.1 * (1 - a) + ScrimField.linear(c.1) * a, lin.2 * (1 - a) + ScrimField.linear(c.2) * a)
                        }
                        if minimum > 0 { over((tint.x, tint.y, tint.z), minimum) }
                        if let flat {
                            over((flat.0.x, flat.0.y, flat.0.z), flat.1)
                        } else {
                            let a = scrim[i * 4 + 3]
                            if a > 0 { over((scrim[i * 4] / a, scrim[i * 4 + 1] / a, scrim[i * 4 + 2] / a), a) }
                        }
                        let shown = [ScrimField.luminance(enc.0, enc.1, enc.2), 0.2126 * lin.0 + 0.7152 * lin.1 + 0.0722 * lin.2]
                        for l in shown { for r in roleL { worst = min(worst, contrast(l, r)) } }
                    }
                    return worst
                }
                let open = worst(base: pixels(layers.base!), minimum: 0, tint: layers.scrimColor, scrim: pixels(layers.scrim!), flat: nil)
                let openFlat = worst(base: pixels(layers.base!), minimum: 0, tint: layers.scrimColor, scrim: [], flat: (layers.scrimColor, layers.scrimPeak))
                let pane = layers.panes[.content]!
                let paneBase = pixels(pane.base!)
                let paneWorst = worst(base: paneBase, minimum: pane.minimum, tint: layers.paneTint, scrim: pixels(pane.scrim!), flat: nil)
                let paneFlat = worst(base: paneBase, minimum: pane.minimum, tint: layers.paneTint, scrim: [], flat: (layers.paneTint, pane.scrimPeak))
                for (name, value) in [("open", open), ("open stale", openFlat), ("pane", paneWorst), ("pane stale", paneFlat)] where value < target {
                    print("FAIL: \(polarity) blur \(blur) \(name) worst contrast \(value) < \(target)")
                    failures += 1
                }
            }
        }
        if failures > 0 { exit(1) }
        print("backdrop layers: every point reaches the target under linear and encoded blending, and so does the flat stale scrim")
    }
}
