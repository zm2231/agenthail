import AppKit
import SwiftUI

enum DesktopPalette {
    static let side = dynamic(light: 0xF5F4F1, dark: 0x181716)
    static let window = dynamic(light: 0xFFFFFF, dark: 0x1C1B1A)
    static let raised = dynamic(light: 0xFFFFFF, dark: 0x24221F)
    static let line = dynamic(light: 0xE3E0DB, dark: 0x33302D)
    static let line2 = dynamic(light: 0xEEEBE7, dark: 0x2A2826)
    static let text = dynamic(light: 0x1D1B19, dark: 0xEDEAE6)
    static let text2 = dynamic(light: 0x5F5954, dark: 0xAEA79F)
    static let bubble = dynamic(light: 0xF1EFEC, dark: 0x2B2926)
    static let selection = dynamic(light: 0xE9E6E2, dark: 0x2C2A27)
    static let accent = dynamic(light: 0xC2410C, dark: 0xF07A45)
    static let onAccent = dynamic(light: 0xFFFFFF, dark: 0x1F1208)
    static let accentText = dynamic(light: 0xB03A0B, dark: 0xF3A27A)
    static let green = dynamic(light: 0x1F7A3A, dark: 0x62C383)
    static let amber = dynamic(light: 0xB45309, dark: 0xE6A24F)
    static let red = dynamic(light: 0xB42318, dark: 0xF28A7E)
    static let muted = dynamic(light: 0xA39C95, dark: 0x6F6963)
    static let work = dynamic(light: 0x5B7FA8, dark: 0x8FB0D6)
    static let dock = dynamic(light: 0xF7F6F4, dark: 0x211F1D)
    static let warnBackground = dynamic(light: 0xFFF4E5, dark: 0x33261A)
    static let warnLine = dynamic(light: 0xF0D9B5, dark: 0x5A4224)
    static let stop = dynamic(light: 0x3F3A36, dark: 0x4A4643)

    static func statusColor(_ session: SessionState, needsYou: Bool) -> Color {
        if needsYou { return amber }
        if session.isWorking { return work }
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
