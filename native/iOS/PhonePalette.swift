import SwiftUI
import UIKit

enum PhonePalette {
    static let attention = RoleStyle(.needsYou)
    static let working = RoleStyle(.working)
    static let done = RoleStyle(.done)
    static let failed = RoleStyle(.error)
    static let accent = PhoneAccentStyle(use: .fill)
    static let accentText = PhoneAccentStyle(use: .text)
    static let ground = Color(uiColor: UIColor { traits in
        let hex = traits.userInterfaceStyle == .dark ? PaletteTokens.window.dark : PaletteTokens.window.light
        return UIColor(red: CGFloat((hex >> 16) & 0xFF) / 255, green: CGFloat((hex >> 8) & 0xFF) / 255, blue: CGFloat(hex & 0xFF) / 255, alpha: 1)
    })
    static let hairline = Color(uiColor: UIColor { $0.userInterfaceStyle == .dark ? UIColor(white: 1, alpha: 0.10) : UIColor(white: 0, alpha: 0.08) })

    static func ground(dark: Bool) -> BackdropRGB { rgb(dark ? PaletteTokens.window.dark : PaletteTokens.window.light) }

    static func textRoles(_ colors: TextColors, dark: Bool, increased: Bool) -> [BackdropRGB] {
        (colors.roles(dark: dark, increased: increased) + [PhoneAccentStyle.hex(colors, use: .text, dark: dark, increased: increased)]).map(rgb)
    }

    static func rgb(_ hex: UInt32) -> BackdropRGB {
        BackdropRGB(Float((hex >> 16) & 0xFF) / 255, Float((hex >> 8) & 0xFF) / 255, Float(hex & 0xFF) / 255)
    }
}
