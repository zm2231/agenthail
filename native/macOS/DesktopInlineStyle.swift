import SwiftUI

extension View {
    func desktopInlineStyle() -> some View {
        modifier(RoleInlineStyle(codeScale: 0.94) { colors, dark, increased in
            DesktopStyle.accent(.text, colors: colors, dark: dark, increased: increased)
        })
    }
}
