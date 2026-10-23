@testable import AgenthailHelpers
import Foundation

@main
struct ContrastVectorsTest {
    static let fixture = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        .appendingPathComponent("scripts/fixtures/contrast-vectors.json")

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
