import SwiftUI

extension View {
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
    @Environment(\.accessibilityReduceTransparency) private var reduceTransparency

    var body: some View {
        let solid = appearance.config.solidSurfaces(reduceTransparency: reduceTransparency)
        content
            .toolbarBackground(PhonePalette.ground, for: .tabBar)
            .toolbarBackgroundVisibility(solid ? .visible : .automatic, for: .tabBar)
    }
}
