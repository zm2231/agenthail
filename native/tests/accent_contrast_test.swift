@testable import AgenthailHelpers

@main
struct AccentContrastTest {
    static func main() {
        typealias RGB = AccentContrast.RGB
        let accents: [(String, UInt32)] = [
            ("blue", 0x007AFF), ("purple", 0xA550A7), ("pink", 0xF74F9E), ("red", 0xFF5257),
            ("orange", 0xF7821B), ("yellow", 0xFFC600), ("green", 0x62BA46), ("graphite", 0x8C8C8C),
            ("dark blue", 0x0A84FF), ("dark yellow", 0xFFD60A)
        ]
        let light = [0xFFFFFF, 0xF3F3F4, 0xFFFFFF].map(RGB.init(hex:))
        let dark = [0x17181A, 0x1D1E21, 0x232427].map(RGB.init(hex:))
        for (name, hex) in accents {
            let accent = RGB(hex: hex)
            for (surfaces, isDark) in [(light, false), (dark, true)] {
                let text = AccentContrast.readable(accent, on: surfaces, dark: isDark)
                for surface in surfaces {
                    precondition(AccentContrast.contrast(text, surface) >= 4.5, "\(name) text on \(isDark ? "dark" : "light") surface")
                }
            }
            let foreground = AccentContrast.foreground(on: accent)
            precondition(AccentContrast.contrast(foreground, accent) >= 3, "\(name) foreground")
        }
        let blue = RGB(hex: 0x007AFF)
        precondition(AccentContrast.readable(blue, on: dark, dark: true) != blue || AccentContrast.contrast(blue, RGB(hex: 0x232427)) >= 4.5)
        let alreadyReadable = RGB(hex: 0x1A4FA0)
        precondition(AccentContrast.readable(alreadyReadable, on: light, dark: false) == alreadyReadable, "readable accents stay unchanged")
        precondition(AccentContrast.foreground(on: RGB(hex: 0xFFC600)) == .black)
        precondition(AccentContrast.foreground(on: RGB(hex: 0x0040DD)) == .white)
        print("ok")
    }
}
