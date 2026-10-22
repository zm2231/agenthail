import AppKit
import SwiftUI

enum DesktopPalette {
    static let side = dynamic(light: 0xF3F3F4, dark: 0x1D1E21)
    static let window = dynamic(light: 0xFFFFFF, dark: 0x17181A)
    static let raised = dynamic(light: 0xFFFFFF, dark: 0x232427)
    static let line = dynamic(light: 0xE3E3E6, dark: 0x2E2F33)
    static let line2 = dynamic(light: 0xECECEE, dark: 0x26272A)
    static let text = dynamic(light: 0x1D1D1F, dark: 0xECECEE)
    static let text2 = dynamic(light: 0x5F6066, dark: 0xA3A3A8)
    static let muted = dynamic(light: 0x9A9BA1, dark: 0x6F7075)
    static let bubble = dynamic(light: 0xF1F1F3, dark: 0x26272A)
    static let fill = dynamic(light: 0xE9E9EC, dark: 0x2A2B2F)
    static let dock = dynamic(light: 0xF7F7F8, dark: 0x1B1C1F)
    static let stop = dynamic(light: 0x3D3E42, dark: 0x48494D)

    static let accent = Color(nsColor: .controlAccentColor)
    static let accentText = Color(nsColor: .controlAccentColor)
    static let onAccent = Color.white
    static let selection = Color(nsColor: NSColor.controlAccentColor.withAlphaComponent(0.18))

    static let attention = dynamic(light: 0xB26B00, dark: 0xFFB224)
    static let attentionBackground = dynamic(light: 0xFFF5E0, dark: 0x2E2414)
    static let attentionLine = dynamic(light: 0xF2D9A6, dark: 0x5C4415)
    static let working = dynamic(light: 0x2F6BD8, dark: 0x7AA7FF)
    static let done = dynamic(light: 0x1A7F4E, dark: 0x4CC38A)
    static let failed = dynamic(light: 0xC4271B, dark: 0xFF6B61)
    static let failedBackground = dynamic(light: 0xFDEDEC, dark: 0x2E1A19)
    static let failedLine = dynamic(light: 0xF5C9C5, dark: 0x5A2B28)

    static func statusColor(_ session: SessionState, needsYou: Bool, finishedUnseen: Bool = false) -> Color {
        if needsYou { return attention }
        if session.isWorking { return working }
        if finishedUnseen { return done }
        return muted
    }

    private static func dynamic(light: UInt32, dark: UInt32) -> Color {
        Color(nsColor: NSColor(name: nil) { appearance in
            let isDark = appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
            return color(isDark ? dark : light)
        })
    }

    private static func color(_ hex: UInt32) -> NSColor {
        NSColor(
            srgbRed: CGFloat((hex >> 16) & 0xFF) / 255,
            green: CGFloat((hex >> 8) & 0xFF) / 255,
            blue: CGFloat(hex & 0xFF) / 255,
            alpha: 1
        )
    }
}

enum DesktopType {
    static let caption: CGFloat = 11
    static let meta: CGFloat = 11.5
    static let secondary: CGFloat = 12
    static let body: CGFloat = 13
    static let prose: CGFloat = 14.5
    /// Extra leading that gives prose a 1.58 line height.
    static let proseLineSpacing: CGFloat = 6
    static let title: CGFloat = 15
    static let display: CGFloat = 20
}

enum DesktopSpace {
    static let xs: CGFloat = 4
    static let s: CGFloat = 8
    static let m: CGFloat = 12
    static let l: CGFloat = 16
    static let xl: CGFloat = 20
    static let xxl: CGFloat = 24
    static let xxxl: CGFloat = 32
}

enum DesktopRadius {
    static let control: CGFloat = 6
    static let card: CGFloat = 10
    static let sheet: CGFloat = 14
}
