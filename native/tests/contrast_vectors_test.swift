@testable import AgenthailHelpers
import CoreGraphics
import Foundation

@main
struct ContrastVectorsTest {
    static let fixture = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        .appendingPathComponent("scripts/fixtures/contrast-vectors.json")

    static func bytes(_ image: CGImage) -> [UInt8] {
        let w = image.width, h = image.height
        var out = [UInt8](repeating: 0, count: w * h * 4)
        let ctx = CGContext(data: &out, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4, space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
        ctx.draw(image, in: CGRect(x: 0, y: 0, width: w, height: h))
        return out
    }

    static func backdrop() -> [String: Any] {
        let colours: [[Float]] = [[0.1, 0.2, 0.5], [0.9, 0.3, 0.2], [0.2, 0.8, 0.4], [0.05, 0.05, 0.1], [1, 1, 1], [0.5, 0.1, 0.6], [0.3, 0.3, 0.3], [0.95, 0.85, 0.2], [0, 0.4, 0.9]]
        let mesh = BackdropRenderer.mesh(columns: 3, rows: 3, colors: colours.map { BackdropRGB($0[0], $0[1], $0[2]) }, aspect: 1.6)!
        let pixels = bytes(mesh)
        let points = [(0, 0), (191, 0), (0, mesh.height - 1), (191, mesh.height - 1), (96, mesh.height / 2), (40, 17), (150, 90), (77, 101)]
        let grain = bytes(BackdropRenderer.grain()!)
        return [
            "mesh": [
                "columns": 3, "rows": 3, "aspect": 1.6, "colours": colours, "width": mesh.width, "height": mesh.height,
                "samples": points.map { p -> [String: Any] in
                    let i = (p.1 * mesh.width + p.0) * 4
                    return ["x": p.0, "y": p.1, "rgb": [pixels[i], pixels[i + 1], pixels[i + 2]]]
                },
            ],
            "grain": [
                "size": 256,
                "first": (0..<32).map { grain[$0 * 4] },
                "sum": (0..<(256 * 256)).reduce(0) { $0 + Int(grain[$1 * 4]) },
            ],
        ]
    }

    static func vectors() -> [String: Any] {
        let channels: [Float] = [0, 0.02, 0.04045, 0.1, 0.25, 0.5, 0.75, 0.9, 1]
        let levels: [Float] = [0, 0.001, 0.0031308, 0.01, 0.1, 0.2, 0.5, 0.8, 1]
        let colours: [[Float]] = [[0, 0, 0], [1, 1, 1], [0.09, 0.094, 0.102], [0.925, 0.925, 0.933], [0.98, 0.42, 0.25], [0.2, 0.6, 0.9], [0.5, 0.5, 0.5]]
        let roleSets: [[Float]] = [[0.85, 0.42, 0.25], [0.012, 0.09, 0.15], [0.3]]
        var boundaries: [[String: Any]] = []
        for roles in roleSets {
            for polarity in [ScrimField.Polarity.dark, .light] {
                for target: Float in [4.5, 7] {
                    boundaries.append(["roles": roles, "polarity": polarity == .dark ? "dark" : "light", "target": target,
                                       "out": ScrimField.boundary(roles: roles, polarity: polarity, target: target)])
                }
            }
        }
        var reachable: [[String: Any]] = []
        for colour in colours {
            for polarity in [ScrimField.Polarity.dark, .light] {
                for boundary: Float in [0.05, 0.18, 0.4] {
                    let r = ScrimField.reachable((colour[0], colour[1], colour[2]), polarity: polarity, boundary: boundary)
                    reachable.append(["colour": colour, "polarity": polarity == .dark ? "dark" : "light", "boundary": boundary, "out": [r.0, r.1, r.2]])
                }
            }
        }
        let surfaces: [Bool: [AccentContrast.RGB]] = [
            true: [AccentContrast.RGB(hex: 0x17181A), AccentContrast.RGB(hex: 0x1F2023), AccentContrast.RGB(hex: 0x2A2B2F)],
            false: [AccentContrast.RGB(hex: 0xFFFFFF), AccentContrast.RGB(hex: 0xF5F5F7), AccentContrast.RGB(hex: 0xE8E8ED)],
        ]
        var accents: [[String: Any]] = []
        for hex: UInt32 in [0x0A84FF, 0x1B2A6B, 0xE9A23B, 0xF2F2F2, 0x3FB68B, 0x101010] {
            for dark in [true, false] {
                let accent = AccentContrast.RGB(hex: hex)
                let readable = AccentContrast.readable(accent, on: surfaces[dark]!, dark: dark)
                let foreground = AccentContrast.foreground(on: readable)
                accents.append([
                    "accent": [accent.r, accent.g, accent.b], "dark": dark,
                    "surfaces": surfaces[dark]!.map { [$0.r, $0.g, $0.b] },
                    "readable": [readable.r, readable.g, readable.b], "foreground": [foreground.r, foreground.g, foreground.b],
                ])
            }
        }
        return [
            "accent": accents,
            "backdrop": backdrop(),
            "margin": ScrimField.margin,
            "tintDepth": ScrimField.tintDepth,
            "linear": channels.map { ["in": $0, "out": ScrimField.linear($0)] },
            "gamma": levels.map { ["in": $0, "out": ScrimField.gamma($0)] },
            "luminance": colours.map { ["in": $0, "out": ScrimField.luminance($0[0], $0[1], $0[2])] },
            "boundary": boundaries,
            "reachable": reachable,
        ]
    }

    static func main() throws {
        let data = try JSONSerialization.data(withJSONObject: vectors(), options: [.prettyPrinted, .sortedKeys])
        if ProcessInfo.processInfo.environment["AGENTHAIL_WRITE_VECTORS"] == "1" {
            try (data + Data("\n".utf8)).write(to: fixture)
            print("wrote \(fixture.path)")
            return
        }
        let stored = try Data(contentsOf: fixture)
        let want = try JSONSerialization.jsonObject(with: data) as! NSDictionary
        let have = try JSONSerialization.jsonObject(with: stored) as! NSDictionary
        guard want == have else {
            print("FAIL: \(fixture.path) no longer matches ScrimField; rerun with AGENTHAIL_WRITE_VECTORS=1 and update the dashboard's ui-contrast.js to match")
            exit(1)
        }
    }
}
