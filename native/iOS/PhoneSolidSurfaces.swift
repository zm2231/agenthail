import SwiftUI

extension View {
    func solidFloating<S: Shape>(in shape: S) -> some View {
        background(PhonePalette.ground, in: shape)
            .overlay(shape.stroke(PhonePalette.hairline))
            .shadow(color: .black.opacity(0.12), radius: 12, y: 4)
    }

    func solidTabBar() -> some View {
        modifier(SolidTabBar())
    }
}

private struct SolidTabBar: ViewModifier {
    @Environment(\.phoneAppearance) private var appearance

    func body(content: Content) -> some View {
        if let appearance {
            SolidTabBarBody(content: content, appearance: appearance)
        } else {
            content
        }
    }
}

private struct SolidTabBarBody<Content: View>: View {
    let content: Content
    @ObservedObject var appearance: PhoneAppearance
    @Environment(\.colorScheme) private var colorScheme
    @Environment(\.accessibilityReduceTransparency) private var reduceTransparency

    var body: some View {
        let solid = appearance.config.solidSurfaces(dark: colorScheme == .dark, reduceTransparency: reduceTransparency)
        content
            .toolbarBackground(PhonePalette.ground, for: .tabBar)
            .toolbarBackgroundVisibility(solid ? .visible : .automatic, for: .tabBar)
    }
}
