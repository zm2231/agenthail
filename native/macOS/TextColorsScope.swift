import SwiftUI

private struct TextColorsScope: ViewModifier {
    @ObservedObject var interface: InterfaceStore

    func body(content: Content) -> some View {
        content.environment(\.textColors, TextColors(interface.config))
    }
}

extension View {
    func textColors(_ interface: InterfaceStore) -> some View {
        modifier(TextColorsScope(interface: interface))
    }
}
