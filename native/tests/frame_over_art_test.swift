import CoreGraphics
import Foundation
import ImageIO
@testable import AgenthailHelpers

@main
struct FrameOverArtTest {
    static func art(_ name: String) -> WallpaperImage? {
        let url = URL(fileURLWithPath: #filePath).deletingLastPathComponent().appendingPathComponent("../../internal/uiconf/art/\(name).jpg").standardized
        guard let source = CGImageSourceCreateWithURL(url as CFURL, nil), let image = CGImageSourceCreateImageAtIndex(source, 0, nil) else { return nil }
        return WallpaperImage(id: name, image: image)
    }

    static func rgb(_ c: AccentContrast.RGB) -> BackdropRGB { BackdropRGB(Float(c.r), Float(c.g), Float(c.b)) }

    static func worst(_ image: CGImage, scale: CGFloat, in rect: CGRect, roles: [BackdropRGB], plate: (BackdropRGB, Float)? = nil) -> Float {
        let w = image.width, h = image.height
        var data = [UInt8](repeating: 0, count: w * h * 4)
        data.withUnsafeMutableBytes { raw in
            let ctx = CGContext(data: raw.baseAddress, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4, space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
            ctx.draw(image, in: CGRect(x: 0, y: 0, width: w, height: h))
        }
        let lums = roles.map { ScrimField.luminance($0.x, $0.y, $0.z) }
        var worst: Float = .infinity
        for y in Int(rect.minY * scale)..<Int(rect.maxY * scale) {
            for x in Int(rect.minX * scale)..<Int(rect.maxX * scale) {
                let i = (y * w + x) * 4
                var pixel = (Float(data[i]) / 255, Float(data[i + 1]) / 255, Float(data[i + 2]) / 255)
                if let (color, alpha) = plate {
                    pixel = (pixel.0 + (color.x - pixel.0) * alpha, pixel.1 + (color.y - pixel.1) * alpha, pixel.2 + (color.z - pixel.2) * alpha)
                }
                let bg = ScrimField.luminance(pixel.0, pixel.1, pixel.2)
                for role in lums { worst = min(worst, (max(role, bg) + 0.05) / (min(role, bg) + 0.05)) }
            }
        }
        return worst
    }

    static func main() {
        let names = ["field", "tide", "aurora", "dawn", "ink-ridges", "coast", "fog-forest", "still-sea", "dunes", "concrete", "arches", "night-sky", "nebula", "wave", "caustics", "ink-water"]
        let config = UIConfig()
        let size = CGSize(width: 480, height: 300), scale: CGFloat = 1
        let chrome = CGRect(x: 0, y: 0, width: 480, height: 40)
        let sidebar = CGRect(x: 0, y: 40, width: 160, height: 260)
        let content = CGRect(x: 160, y: 40, width: 320, height: 260)
        var failures = 0
        if config.gutter != 0 || config.solidSurfaces(dark: true, reduceTransparency: false) || !config.solidSurfaces(dark: false, reduceTransparency: false) {
            print("FAIL: the default frame should join glass panes edge to edge in dark and draw solid panes in light, got gutter \(config.gutter)")
            failures += 1
        }
        let ground = rgb(DesktopPalette.ground(dark: true))
        let roles = DesktopGlass.roles(config, dark: true, increased: false).map(rgb)
        let tint = Float(config.paneOpacity)
        let panes = [("chrome", chrome), ("sidebar", sidebar), ("content", content)].map { BackdropSpec.Pane(id: $0.0, rect: $0.1, radius: 0, minimum: tint, strength: 1) }
        let texts = [chrome, sidebar, content].map { $0.insetBy(dx: 8, dy: 8) }
        for name in names {
            guard let wallpaper = art("\(name)-dark") else {
                print("FAIL: \(name) art did not load")
                failures += 1
                continue
            }
            let spec = BackdropSpec(wallpaper: .image(wallpaper), texture: "none", blur: CGFloat(config.glassBlur), paneTint: ground, polarity: .dark, ground: ground, roles: roles,
                                    size: size, scale: scale, panes: panes, texts: texts, paneTexts: texts, readability: true)
            guard let rendered = BackdropRenderer.render(spec) else {
                print("FAIL: \(name) did not render")
                failures += 1
                continue
            }
            for text in texts {
                for (state, plate) in [("", nil), (" selected", (ground, Float(0.6)))] as [(String, (BackdropRGB, Float)?)] {
                    let contrast = worst(rendered.image, scale: scale, in: text, roles: roles, plate: plate)
                    if contrast < Float(config.contrastTarget) {
                        print("FAIL: \(name)\(state) text at \(text) reads at \(contrast):1")
                        failures += 1
                    }
                }
            }
        }
        if failures > 0 { exit(1) }
        print("frame over art: every text role reaches the target on every dark glass pane over every built-in picture, selected or not")
    }
}
