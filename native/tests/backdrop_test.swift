import CoreGraphics
import Foundation
@testable import AgenthailHelpers

@main
struct BackdropTest {
    static func hostileWallpaper(width: Int, height: Int) -> WallpaperImage {
        let ctx = CGContext(data: nil, width: width, height: height, bitsPerComponent: 8, bytesPerRow: width * 4, space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
        let pixels = ctx.data!.bindMemory(to: UInt8.self, capacity: width * height * 4)
        var seed: UInt32 = 11
        for y in 0..<height {
            for x in 0..<width {
                let dx = Float(x) - Float(width) * 0.7, dy = Float(y) - Float(height) * 0.3
                let glow = expf(-(dx * dx + dy * dy) / (Float(width * width) * 0.03))
                let sky = Float(y) / Float(height)
                var c = (min(0.3 + 0.6 * sky + glow, 1), min(0.15 + 0.4 * sky + glow, 1), min(0.4 + 0.1 * sky + glow, 1))
                seed = seed &* 1_103_515_245 &+ 12345
                if seed % 61 == 0 { c = (1, 1, 1) }
                let i = (y * width + x) * 4
                pixels[i] = UInt8(c.0 * 255); pixels[i + 1] = UInt8(c.1 * 255); pixels[i + 2] = UInt8(c.2 * 255); pixels[i + 3] = 255
            }
        }
        return WallpaperImage(id: "hostile", image: ctx.makeImage()!)
    }

    static func rgb(_ hex: UInt32) -> BackdropRGB {
        BackdropRGB(Float((hex >> 16) & 0xFF) / 255, Float((hex >> 8) & 0xFF) / 255, Float(hex & 0xFF) / 255)
    }

    static func pixels(_ image: CGImage) -> [UInt8] {
        let w = image.width, h = image.height
        var data = [UInt8](repeating: 0, count: w * h * 4)
        data.withUnsafeMutableBytes { raw in
            let ctx = CGContext(data: raw.baseAddress, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4, space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
            ctx.draw(image, in: CGRect(x: 0, y: 0, width: w, height: h))
        }
        return data
    }

    static func worst(_ image: CGImage, scale: CGFloat, in rects: [CGRect], roles: [BackdropRGB]) -> Float {
        let data = pixels(image)
        let w = image.width
        let lums = roles.map { ScrimField.luminance($0.x, $0.y, $0.z) }
        var worst: Float = .infinity
        for rect in rects {
            for y in Int(rect.minY * scale)..<Int(rect.maxY * scale) {
                for x in Int(rect.minX * scale)..<Int(rect.maxX * scale) {
                    let i = (y * w + x) * 4
                    let bg = ScrimField.luminance(Float(data[i]) / 255, Float(data[i + 1]) / 255, Float(data[i + 2]) / 255)
                    for role in lums { worst = min(worst, (max(role, bg) + 0.05) / (min(role, bg) + 0.05)) }
                }
            }
        }
        return worst
    }

    static func mean(_ image: CGImage, scale: CGFloat, in rect: CGRect) -> Float {
        let data = pixels(image)
        var sum: Float = 0, count: Float = 0
        for y in Int(rect.minY * scale)..<Int(rect.maxY * scale) {
            for x in Int(rect.minX * scale)..<Int(rect.maxX * scale) {
                let i = (y * image.width + x) * 4
                sum += ScrimField.luminance(Float(data[i]) / 255, Float(data[i + 1]) / 255, Float(data[i + 2]) / 255)
                count += 1
            }
        }
        return sum / count
    }

    /// One white device pixel in every other point, on black, at scale 2:
    /// neighbouring points need very different opacities, so any pixel
    /// interpolated from too low a neighbour shows up.
    static func spikes() -> Int {
        let size = CGSize(width: 120, height: 80), scale: CGFloat = 2
        let w = Int(size.width * scale), h = Int(size.height * scale)
        let ctx = CGContext(data: nil, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4, space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
        let pixels = ctx.data!.bindMemory(to: UInt8.self, capacity: w * h * 4)
        for y in 0..<h {
            for x in 0..<w {
                let white: UInt8 = (x % 4 == 1 && y % 4 == 1) ? 255 : 0
                let i = (y * w + x) * 4
                pixels[i] = white; pixels[i + 1] = white; pixels[i + 2] = white; pixels[i + 3] = 255
            }
        }
        let roles = [0xECECEE, 0x8E8E93].map(rgb)
        let ground = rgb(0x17181A)
        let pane = CGRect(x: 4, y: 4, width: 50, height: 72), text = CGRect(x: 70, y: 30, width: 40, height: 16)
        let spec = BackdropSpec(wallpaper: .image(WallpaperImage(id: "spikes", image: ctx.makeImage()!)), fit: "center", texture: "none", blur: 0, paneTint: ground, ground: ground, roles: roles, size: size, scale: scale,
                                panes: [.init(id: "pane", rect: pane, radius: 0, minimum: 0, strength: 1)], texts: [text])
        guard let out = BackdropRenderer.render(spec) else { print("FAIL: spikes render returned nil"); return 1 }
        let result = worst(out.image, scale: scale, in: [pane, text], roles: roles)
        if result < 4.5 {
            print("FAIL: one-pixel spikes at scale 2 read at \(result):1")
            return 1
        }
        return 0
    }

    /// A checkerboard of 0 and 1 at scale 2: every pixel of a 1 point
    /// borders a 0 point, so plain interpolation would read 0.75 there.
    static func masks() -> Int {
        let pw = 20, ph = 20, scale = 2
        let values = (0..<(pw * ph)).map { Float(($0 % pw + $0 / pw) % 2) }
        let w = pw * scale, h = ph * scale
        let image = BackdropRenderer.pixelMask(values, width: pw, height: ph, pixels: CGSize(width: w, height: h))
        var out = [Float](repeating: 0, count: w * h * 4)
        out.withUnsafeMutableBytes { BackdropRenderer.context.render(image, toBitmap: $0.baseAddress!, rowBytes: w * 16, bounds: CGRect(x: 0, y: 0, width: w, height: h), format: .RGBAf, colorSpace: nil) }
        var lowest: Float = 1
        for y in 0..<h {
            for x in 0..<w {
                let point = values[(y / scale) * pw + x / scale]
                if point == 1 { lowest = min(lowest, out[(y * w + x) * 4]) }
            }
        }
        if lowest < 0.999 {
            print("FAIL: upsampled mask reads \(lowest) inside a point that needs 1")
            return 1
        }
        return 0
    }

    static func main() {
        let size = CGSize(width: 420, height: 260)
        let scale: CGFloat = 2
        let wallpaper = hostileWallpaper(width: 600, height: 400)
        let sidebar = CGRect(x: 8, y: 8, width: 120, height: 244)
        let headline = CGRect(x: 220, y: 60, width: 150, height: 24)
        let composer = CGRect(x: 200, y: 100, width: 190, height: 50)
        let far = CGRect(x: 380, y: 220, width: 30, height: 30)
        let cases: [(ScrimField.Polarity, [BackdropRGB], BackdropRGB)] = [
            (.dark, [0xECECEE, 0xA3A3A8, 0x8E8E93, 0xFFB224, 0x7AA7FF, 0x4CC38A, 0xFF6B61].map(rgb), rgb(0x17181A)),
            (.light, [0x1D1D1F, 0x5F6066, 0x6E6E76, 0x965A00, 0x2A64CC, 0x1A7F4E, 0xC4271B].map(rgb), rgb(0xFFFFFF)),
        ]
        var failures = 0
        for (polarity, roles, ground) in cases {
            for wall in [BackdropSpec.Wallpaper.image(wallpaper), .mesh(columns: 2, rows: 2, colors: [rgb(0xFFE0F0), rgb(0x203060), rgb(0xF0F0A0), rgb(0x000000)])] {
                for texture in ["scanlines", "grain"] {
                    let spec = BackdropSpec(
                        wallpaper: wall, texture: texture, paneTint: ground, feather: 40, polarity: polarity, ground: ground, roles: roles,
                        size: size, scale: scale,
                        panes: [.init(id: "sidebar", rect: sidebar, radius: 12, minimum: 0.18, strength: 1)],
                        texts: [headline, composer]
                    )
                    guard let out = BackdropRenderer.render(spec) else {
                        print("FAIL: render returned nil for \(polarity) \(texture)")
                        failures += 1
                        continue
                    }
                    let inner = sidebar.insetBy(dx: 12, dy: 12)
                    let paneWorst = worst(out.image, scale: scale, in: [inner], roles: roles)
                    let openWorst = worst(out.image, scale: scale, in: [headline, composer], roles: roles)
                    if paneWorst < 4.5 || openWorst < 4.5 {
                        print("FAIL: \(polarity) \(texture) pane \(paneWorst) open \(openWorst)")
                        failures += 1
                    }
                    if out.image.width != Int(size.width * scale) || out.image.height != Int(size.height * scale) {
                        print("FAIL: image is \(out.image.width)x\(out.image.height)")
                        failures += 1
                    }
                    guard case .image = wall else { continue }
                    let plain = BackdropSpec(wallpaper: wall, texture: texture, paneTint: ground, feather: 40, polarity: polarity, ground: ground, roles: roles, size: size, scale: scale)
                    let bare = BackdropRenderer.render(plain)!
                    let near = abs(mean(out.image, scale: scale, in: headline) - mean(bare.image, scale: scale, in: headline))
                    let away = abs(mean(out.image, scale: scale, in: far) - mean(bare.image, scale: scale, in: far))
                    if !(away < near) {
                        print("FAIL: \(polarity) scrim does not fade away from text: near \(near) far \(away)")
                        failures += 1
                    }
                }
            }
        }
        failures += spikes()
        failures += masks()
        if failures > 0 { exit(1) }
        print("backdrop: every text role reads at 4.5:1 or better on the rendered pixels")
    }
}
