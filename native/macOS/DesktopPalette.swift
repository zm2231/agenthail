import AppKit
import os
import SwiftUI

enum DesktopPalette {
    private static let windowHex: (light: UInt32, dark: UInt32) = (0xFFFFFF, 0x17181A)
    private static let sideHex: (light: UInt32, dark: UInt32) = (0xF3F3F4, 0x1D1E21)
    private static let raisedHex: (light: UInt32, dark: UInt32) = (0xFFFFFF, 0x232427)

    static let side = dynamic(sideHex)
    static let window = dynamic(windowHex)
    static let raised = dynamic(raisedHex)
    static let line = dynamic(light: 0xE3E3E6, dark: 0x2E2F33)
    static let line2 = dynamic(light: 0xECECEE, dark: 0x26272A)
    private static let textHex: (light: UInt32, dark: UInt32) = (0x1D1D1F, 0xECECEE)
    private static let text2Hex: (light: UInt32, dark: UInt32) = (0x5F6066, 0xA3A3A8)
    private static let mutedHex: (light: UInt32, dark: UInt32) = (0x6E6E76, 0x8E8E93)
    private static let attentionHex: (light: UInt32, dark: UInt32) = (0x965A00, 0xFFB224)
    private static let workingHex: (light: UInt32, dark: UInt32) = (0x2A64CC, 0x7AA7FF)
    private static let doneHex: (light: UInt32, dark: UInt32) = (0x1A7F4E, 0x4CC38A)
    private static let failedHex: (light: UInt32, dark: UInt32) = (0xC4271B, 0xFF6B61)

    static let text = dynamic(textHex)
    static let text2 = dynamic(text2Hex)
    static let muted = dynamic(mutedHex)
    static let bubble = dynamic(light: 0xF1F1F3, dark: 0x26272A)
    static let fill = dynamic(light: 0xE9E9EC, dark: 0x2A2B2F)
    static let dock = dynamic(light: 0xF7F7F8, dark: 0x1B1C1F)
    static let stop = dynamic(light: 0x3D3E42, dark: 0x48494D)

    /// The accent ui.conf chose; nil follows the system accent. AppKit
    /// resolves colours on whichever thread draws, so this sits behind a lock.
    private static let accentChoice = OSAllocatedUnfairLock<AccentContrast.RGB?>(initialState: nil)

    static func choose(accent: AccentContrast.RGB?) {
        accentChoice.withLock { $0 = accent }
    }

    static let accent = Color(nsColor: NSColor(name: nil) { accentRGB(in: $0).nsColor })
    static let accentText = Color(nsColor: NSColor(name: nil) { appearance in
        readableAccent(in: appearance).nsColor
    })
    static let onAccent = Color(nsColor: NSColor(name: nil) { appearance in
        AccentContrast.foreground(on: accentRGB(in: appearance)).nsColor
    })
    static let selection = Color(nsColor: NSColor(name: nil) { appearance in
        accentRGB(in: appearance).nsColor.withAlphaComponent(0.18)
    })

    static func accentRGB(in appearance: NSAppearance) -> AccentContrast.RGB {
        accentChoice.withLock { $0 } ?? AccentContrast.rgb(.controlAccentColor, in: appearance)
    }

    static func readableAccent(in appearance: NSAppearance) -> AccentContrast.RGB {
        let dark = isDark(appearance)
        let surfaces = [windowHex, sideHex, raisedHex].map { AccentContrast.RGB(hex: dark ? $0.dark : $0.light) }
        return AccentContrast.readable(accentRGB(in: appearance), on: surfaces, dark: dark)
    }

    /// Every colour text is drawn in, which a scrim must keep readable.
    static func textRoles(dark: Bool) -> [AccentContrast.RGB] {
        [textHex, text2Hex, mutedHex, attentionHex, workingHex, doneHex, failedHex].map { AccentContrast.RGB(hex: dark ? $0.dark : $0.light) }
    }

    static func ground(dark: Bool) -> AccentContrast.RGB {
        AccentContrast.RGB(hex: dark ? windowHex.dark : windowHex.light)
    }

    static let attention = dynamic(attentionHex)
    static let attentionBackground = dynamic(light: 0xFFF5E0, dark: 0x2E2414)
    static let attentionLine = dynamic(light: 0xF2D9A6, dark: 0x5C4415)
    static let working = dynamic(workingHex)
    static let done = dynamic(doneHex)
    static let failed = dynamic(failedHex)
    static let failedBackground = dynamic(light: 0xFDEDEC, dark: 0x2E1A19)
    static let failedLine = dynamic(light: 0xF5C9C5, dark: 0x5A2B28)

    static func statusColor(_ session: SessionState, needsYou: Bool, finishedUnseen: Bool = false) -> Color {
        if needsYou { return attention }
        if session.isWorking { return working }
        if finishedUnseen { return done }
        return muted
    }

    private static func dynamic(_ pair: (light: UInt32, dark: UInt32)) -> Color {
        dynamic(light: pair.light, dark: pair.dark)
    }

    private static func dynamic(light: UInt32, dark: UInt32) -> Color {
        Color(nsColor: NSColor(name: nil) { appearance in
            color(isDark(appearance) ? dark : light)
        })
    }

    static func isDark(_ appearance: NSAppearance) -> Bool {
        appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
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

enum DesktopColumns {
    static let sidebar = (min: CGFloat(248), ideal: CGFloat(300), max: CGFloat(340))
    static let detail = (min: CGFloat(340), ideal: CGFloat(892))
    static let inspector = (min: CGFloat(270), ideal: CGFloat(284), max: CGFloat(300))
}

enum ColumnFit {
    /// Narrower than this, a docked inspector would pin the window at the
    /// columns' minimum widths, so it stops docking before the window does.
    static let inspectorDocks = DesktopColumns.sidebar.max + DesktopColumns.detail.min + DesktopColumns.inspector.ideal
    /// The same for the sidebar, once the inspector has collapsed.
    static let sidebarDocks = DesktopColumns.sidebar.max + DesktopColumns.detail.min
    /// The narrowest window, with both side columns collapsed.
    static let windowMinimum: CGFloat = 420
}

enum DesktopRadius {
    static let control: CGFloat = 6
    static let card: CGFloat = 10
    static let sheet: CGFloat = 14
}
