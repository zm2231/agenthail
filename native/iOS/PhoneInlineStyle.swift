import SwiftUI

extension View {
    func phoneInlineStyle() -> some View {
        modifier(RoleInlineStyle(codeScale: 0.85, accentText: PhoneInlineStyle.accentText))
    }
}

enum PhoneInlineStyle {
    static func accentText(_ colors: TextColors, dark: Bool, increased: Bool) -> Color {
        Color(hex: PhoneAccentStyle.hex(colors, use: .text, dark: dark, increased: increased))
    }
}
