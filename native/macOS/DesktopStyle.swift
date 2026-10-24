import AppKit
import SwiftUI

struct DesktopStyle: ShapeStyle {
    enum Accent: Sendable { case fill, text, onAccent, selection }
    enum Kind: Sendable {
        case role(TextRole)
        case accent(Accent)
    }

    let kind: Kind

    static func role(_ role: TextRole) -> DesktopStyle { DesktopStyle(kind: .role(role)) }
    static func accent(_ use: Accent) -> DesktopStyle { DesktopStyle(kind: .accent(use)) }

    func resolve(in environment: EnvironmentValues) -> Color {
        switch kind {
        case .role(let role): return RoleStyle(role).resolve(in: environment)
        case .accent(let use): return Self.accent(use, in: environment)
        }
    }

    static func target(_ colors: TextColors, increased: Bool) -> Double {
        increased ? 7 : colors.target
    }

    private static func accent(_ use: Accent, in environment: EnvironmentValues) -> Color {
        let dark = environment.colorScheme == .dark
        let increased = environment.colorSchemeContrast == .increased
        let colors = environment.textColors
        guard let choice = colors.accentChoice.map(AccentContrast.RGB.init(hex:)) else {
            return system(use, colors: colors, increased: increased)
        }
        switch use {
        case .fill: return choice.color
        case .selection: return choice.color.opacity(0.18)
        case .onAccent: return AccentContrast.foreground(on: choice).color
        case .text:
            if let served = colors.accentText(dark: dark, increased: increased) { return Color(hex: served) }
            return DesktopPalette.readable(choice, dark: dark, target: target(colors, increased: increased)).color
        }
    }

    private static func system(_ use: Accent, colors: TextColors, increased: Bool) -> Color {
        Color(nsColor: NSColor(name: nil) { appearance in
            let dark = DesktopPalette.isDark(appearance)
            let accent = AccentContrast.rgb(.controlAccentColor, in: appearance)
            switch use {
            case .fill: return accent.nsColor
            case .selection: return accent.nsColor.withAlphaComponent(0.18)
            case .onAccent: return AccentContrast.foreground(on: accent).nsColor
            case .text: return DesktopPalette.readable(accent, dark: dark, target: target(colors, increased: increased)).nsColor
            }
        })
    }
}

extension AccentContrast.RGB {
    var color: Color { Color(.sRGB, red: r, green: g, blue: b) }
}
