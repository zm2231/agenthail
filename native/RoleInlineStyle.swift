import SwiftUI
import Textual

struct RoleInlineStyle: ViewModifier {
    typealias AccentText = (TextColors, _ dark: Bool, _ increased: Bool) -> Color

    let codeScale: CGFloat
    let accentText: AccentText
    @Environment(\.textColors) private var colors

    func body(content: Content) -> some View {
        content.textual.inlineStyle(.default
            .code(.monospaced, .fontScale(codeScale), .foregroundColor(dynamic(.code)))
            .link(.foregroundColor(dynamic(.link))))
    }

    private func dynamic(_ role: TextRole) -> DynamicColor {
        DynamicColor(
            light: Self.color(role, colors: colors, dark: false, increased: false, accentText: accentText),
            dark: Self.color(role, colors: colors, dark: true, increased: false, accentText: accentText),
            highContrastLight: Self.color(role, colors: colors, dark: false, increased: true, accentText: accentText),
            highContrastDark: Self.color(role, colors: colors, dark: true, increased: true, accentText: accentText)
        )
    }

    static func color(_ role: TextRole, colors: TextColors, dark: Bool, increased: Bool, accentText: AccentText) -> Color {
        colors.hex(role, dark: dark, increased: increased).map(Color.init(hex:)) ?? accentText(colors, dark, increased)
    }
}
