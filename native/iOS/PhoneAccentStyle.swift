import SwiftUI

struct PhoneAccentStyle: ShapeStyle {
    enum Use: Sendable { case fill, text, prominent, onAccent }

    let use: Use

    func resolve(in environment: EnvironmentValues) -> Color {
        Color(hex: Self.hex(environment.textColors, use: use, dark: environment.colorScheme == .dark, increased: environment.colorSchemeContrast == .increased))
    }

    static func hex(_ colors: TextColors, use: Use, dark: Bool, increased: Bool) -> UInt32 {
        let fill = colors.accentChoice ?? (dark ? SessionStyle.accentHex.dark : SessionStyle.accentHex.light)
        if use == .prominent || use == .onAccent {
            let pair = AccentContrast.prominent(AccentContrast.RGB(hex: fill), target: increased ? 7 : colors.target)
            return (use == .prominent ? pair.fill : pair.ink).hex
        }
        guard use == .text else { return fill }
        return colors.accentText(dark: dark, increased: increased) ?? fill
    }
}
