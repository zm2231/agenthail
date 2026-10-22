import AppKit

enum AccentContrast {
    struct RGB: Equatable {
        var r: Double
        var g: Double
        var b: Double

        init(r: Double, g: Double, b: Double) {
            self.r = r
            self.g = g
            self.b = b
        }

        init(hex: UInt32) {
            self.init(r: Double((hex >> 16) & 0xFF) / 255, g: Double((hex >> 8) & 0xFF) / 255, b: Double(hex & 0xFF) / 255)
        }

        static let white = RGB(r: 1, g: 1, b: 1)
        static let black = RGB(r: 0, g: 0, b: 0)

        var nsColor: NSColor { NSColor(srgbRed: r, green: g, blue: b, alpha: 1) }

        func mixed(with other: RGB, by amount: Double) -> RGB {
            RGB(r: r + (other.r - r) * amount, g: g + (other.g - g) * amount, b: b + (other.b - b) * amount)
        }
    }

    static let minimumTextContrast = 4.5

    static func luminance(_ color: RGB) -> Double {
        func channel(_ value: Double) -> Double {
            value <= 0.03928 ? value / 12.92 : pow((value + 0.055) / 1.055, 2.4)
        }
        return 0.2126 * channel(color.r) + 0.7152 * channel(color.g) + 0.0722 * channel(color.b)
    }

    static func contrast(_ a: RGB, _ b: RGB) -> Double {
        let (la, lb) = (luminance(a), luminance(b))
        return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
    }

    static func readable(_ accent: RGB, on surfaces: [RGB], dark: Bool) -> RGB {
        let target = dark ? RGB.white : RGB.black
        var amount = 0.0
        var color = accent
        while amount < 1, surfaces.contains(where: { contrast(color, $0) < minimumTextContrast }) {
            amount = min(1, amount + 0.05)
            color = accent.mixed(with: target, by: amount)
        }
        return color
    }

    static func foreground(on accent: RGB) -> RGB {
        contrast(.white, accent) >= contrast(.black, accent) ? .white : .black
    }

    static func rgb(_ color: NSColor, in appearance: NSAppearance) -> RGB {
        var resolved = NSColor.black
        appearance.performAsCurrentDrawingAppearance {
            resolved = color.usingColorSpace(.sRGB) ?? .black
        }
        return RGB(r: Double(resolved.redComponent), g: Double(resolved.greenComponent), b: Double(resolved.blueComponent))
    }
}
