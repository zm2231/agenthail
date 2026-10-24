import SwiftUI

struct RoleStyle: ShapeStyle {
    let role: TextRole

    init(_ role: TextRole) {
        self.role = role
    }

    func resolve(in environment: EnvironmentValues) -> Color {
        let dark = environment.colorScheme == .dark
        let increased = environment.colorSchemeContrast == .increased
        if let hex = environment.textColors.hex(role, dark: dark, increased: increased) {
            return Color(hex: hex)
        }
        return Color.accentColor
    }
}

extension ShapeStyle {
    func when(_ shown: Bool) -> AnyShapeStyle {
        shown ? AnyShapeStyle(self) : AnyShapeStyle(Color.clear)
    }
}
