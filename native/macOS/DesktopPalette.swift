import AppKit
import SwiftUI

enum DesktopPalette {
    private static let windowHex = PaletteTokens.window
    private static let sideHex = PaletteTokens.side
    private static let raisedHex = PaletteTokens.raised

    static let side = dynamic(sideHex)
    static let window = dynamic(windowHex)
    static let raised = dynamic(raisedHex)
    static let line = dynamic(light: 0xE3E3E6, dark: 0x2E2F33)
    static let line2 = dynamic(light: 0xECECEE, dark: 0x26272A)

    static let text = DesktopStyle.role(.body)
    static let text2 = DesktopStyle.role(.muted)
    static let muted = DesktopStyle.role(.faint)
    static let bubble = dynamic(PaletteTokens.bubble)
    static let fill = dynamic(PaletteTokens.fill)
    static let dock = dynamic(light: 0xF7F7F8, dark: 0x1B1C1F)
    static let stop = dynamic(light: 0x3D3E42, dark: 0x48494D)

    static let accent = DesktopStyle.accent(.fill)
    static let accentText = DesktopStyle.accent(.text)
    static let onAccent = DesktopStyle.accent(.onAccent)
    static let selection = DesktopStyle.accent(.selection)

    static func accentRGB(_ colors: TextColors, in appearance: NSAppearance) -> AccentContrast.RGB {
        colors.accentChoice.map(AccentContrast.RGB.init(hex:)) ?? AccentContrast.rgb(.controlAccentColor, in: appearance)
    }

    static func readable(_ accent: AccentContrast.RGB, dark: Bool, target: Double) -> AccentContrast.RGB {
        let surfaces = [windowHex, sideHex, raisedHex].map { AccentContrast.RGB(hex: dark ? $0.dark : $0.light) }
        return AccentContrast.readable(accent, on: surfaces, dark: dark, target: target)
    }

    static func readableAccent(_ colors: TextColors, in appearance: NSAppearance, increased: Bool) -> AccentContrast.RGB {
        let dark = isDark(appearance)
        if let served = colors.accentText(dark: dark, increased: increased) { return AccentContrast.RGB(hex: served) }
        return readable(accentRGB(colors, in: appearance), dark: dark, target: DesktopStyle.target(colors, increased: increased))
    }

    static func ground(dark: Bool) -> AccentContrast.RGB {
        AccentContrast.RGB(hex: dark ? windowHex.dark : windowHex.light)
    }

    static let attention = DesktopStyle.role(.needsYou)
    static let attentionBackground = dynamic(light: 0xFFF5E0, dark: 0x2E2414)
    static let attentionLine = dynamic(light: 0xF2D9A6, dark: 0x5C4415)
    static let working = DesktopStyle.role(.working)
    static let done = DesktopStyle.role(.done)
    static let failed = DesktopStyle.role(.error)
    static let failedBackground = dynamic(light: 0xFDEDEC, dark: 0x2E1A19)
    static let failedLine = dynamic(light: 0xF5C9C5, dark: 0x5A2B28)

    static func statusColor(_ session: SessionState, needsYou: Bool, finishedUnseen: Bool = false) -> DesktopStyle {
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
    static let reading: CGFloat = 1040
    static let wide: CGFloat = 1120
    static let gutter: CGFloat = 32

    /// The transcript's column in a pane this wide, and how far a tool run
    /// may reach past its right edge into the gutter.
    static func transcript(pane: CGFloat) -> (column: CGFloat, overhang: CGFloat) {
        let column = max(min(pane - 2 * gutter, reading), 0)
        let spare = max((pane - 2 * gutter - column) / 2, 0)
        return (column, min(spare, wide - reading))
    }
}

enum ColumnFit {
    /// Narrower than this, a docked inspector would pin the window at the
    /// columns' minimum widths, so it stops docking before the window does.
    static let inspectorDocks = DesktopColumns.sidebar.max + DesktopColumns.detail.min + DesktopColumns.inspector.ideal
    static let inspectorOpens: CGFloat = 1600
    /// The same for the sidebar, once the inspector has collapsed.
    static let sidebarDocks = DesktopColumns.sidebar.max + DesktopColumns.detail.min
}

enum DesktopRadius {
    static let control: CGFloat = 6
    static let card: CGFloat = 10
    static let sheet: CGFloat = 14
}
