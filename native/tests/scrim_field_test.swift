import Foundation
@testable import AgenthailHelpers

@main
struct ScrimFieldTest {
    static func hostileImage(width: Int, height: Int) -> ScrimField.Raster {
        var rgb = [Float](repeating: 0, count: width * height * 3)
        var seed: UInt32 = 7
        for y in 0..<height {
            for x in 0..<width {
                let dx = Float(x) - Float(width) * 0.63, dy = Float(y) - Float(height) * 0.55
                let glow = expf(-(dx * dx + dy * dy) / (Float(width) * Float(width) * 0.02))
                let sky = Float(y) / Float(height)
                let i = (y * width + x) * 3
                rgb[i] = min(0.2 + 0.7 * sky + glow, 1)
                rgb[i + 1] = min(0.1 + 0.4 * sky + glow, 1)
                rgb[i + 2] = min(0.35 + 0.1 * sky + glow * 0.8, 1)
                seed = seed &* 1_103_515_245 &+ 12345
                if seed % 97 == 0 { rgb[i] = 1; rgb[i + 1] = 1; rgb[i + 2] = 1 }
            }
        }
        return ScrimField.Raster(width: width, height: height, rgb: rgb)
    }

    static func roles(_ hexes: [UInt32]) -> [Float] {
        hexes.map { ScrimField.luminance(Float(($0 >> 16) & 0xFF) / 255, Float(($0 >> 8) & 0xFF) / 255, Float($0 & 0xFF) / 255) }
    }

    static func worst(_ shown: ScrimField.Raster, texts: [ScrimField.Rect], roles: [Float]) -> Float {
        var worst: Float = .infinity
        for text in texts {
            for y in text.y..<(text.y + text.height) {
                for x in text.x..<(text.x + text.width) {
                    let i = (y * shown.width + x) * 3
                    let bg = ScrimField.luminance(shown.rgb[i], shown.rgb[i + 1], shown.rgb[i + 2])
                    for role in roles {
                        worst = min(worst, (max(role, bg) + 0.05) / (min(role, bg) + 0.05))
                    }
                }
            }
        }
        return worst
    }

    static func main() {
        let width = 360, height = 240
        let image = hostileImage(width: width, height: height)
        let dark = roles([0xECECEE, 0xA3A3A8, 0x8E8E93, 0xFFB224, 0x7AA7FF, 0x4CC38A, 0xFF6B61, 0x4DA3F5])
        let light = roles([0x1D1D1F, 0x5F6066, 0x6E6E76, 0x965A00, 0x2A64CC, 0x1A7F4E, 0xC4271B, 0x1A62BD])
        let pane = [ScrimField.Rect(x: 20, y: 20, width: 120, height: 200)]
        let open = [ScrimField.Rect(x: 170, y: 90, width: 160, height: 30), ScrimField.Rect(x: 170, y: 130, width: 160, height: 60)]

        for (polarity, roleSet) in [(ScrimField.Polarity.dark, dark), (.light, light)] {
            let boundary = ScrimField.boundary(roles: roleSet, polarity: polarity, target: 4.5)
            for mode in [ScrimField.Tint.auto, polarity == .dark ? .color(0.05, 0.05, 0.05) : .color(0.98, 0.98, 0.98)] {
                let tint = ScrimField.tint(for: image, mode: mode, polarity: polarity, boundary: boundary)
                let paneNeed = ScrimField.needs(image: image, tint: tint, texts: pane, polarity: polarity, boundary: boundary)
                let paneAlpha = ScrimField.paneOpacity(need: paneNeed, width: width, height: height, minimum: 0.18)
                let paneShown = ScrimField.composite(image, tint: tint, alpha: paneAlpha)
                let paneWorst = worst(paneShown, texts: pane, roles: roleSet)
                precondition(paneWorst >= 4.5, "pane \(polarity) \(mode): \(paneWorst)")

                let openNeed = ScrimField.needs(image: image, tint: tint, texts: open, polarity: polarity, boundary: boundary)
                let openAlpha = ScrimField.openOpacity(need: openNeed, width: width, height: height, feather: 240, minimum: 0)
                let openShown = ScrimField.composite(image, tint: tint, alpha: openAlpha)
                let openWorst = worst(openShown, texts: open, roles: roleSet)
                precondition(openWorst >= 4.5, "open \(polarity) \(mode): \(openWorst)")
                precondition(openAlpha[5 * width + 5] < openAlpha[100 * width + 250], "the scrim fades away from text")
                precondition(paneAlpha.allSatisfy { $0 >= 0.18 }, "the person's opacity is a minimum")
            }
        }

        let dim = ScrimField.Raster(width: 40, height: 40, fill: (0.02, 0.02, 0.03))
        let boundary = ScrimField.boundary(roles: dark, polarity: .dark, target: 4.5)
        let need = ScrimField.needs(image: dim, tint: ScrimField.tint(for: dim, mode: .auto, polarity: .dark, boundary: boundary), texts: [ScrimField.Rect(x: 0, y: 0, width: 40, height: 40)], polarity: .dark, boundary: boundary)
        precondition(need.allSatisfy { $0 == 0 }, "a background that already reads needs no scrim")
        print("scrim field ok")
    }
}
