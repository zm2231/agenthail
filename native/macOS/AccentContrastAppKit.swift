import AppKit

extension AccentContrast.RGB {
    var nsColor: NSColor { NSColor(srgbRed: r, green: g, blue: b, alpha: 1) }
}

extension AccentContrast {
    static func rgb(_ color: NSColor, in appearance: NSAppearance) -> RGB {
        var resolved = NSColor.black
        appearance.performAsCurrentDrawingAppearance {
            resolved = color.usingColorSpace(.sRGB) ?? .black
        }
        return RGB(r: Double(resolved.redComponent), g: Double(resolved.greenComponent), b: Double(resolved.blueComponent))
    }
}
