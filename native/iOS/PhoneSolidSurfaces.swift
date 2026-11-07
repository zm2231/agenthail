import SwiftUI

extension View {
    func solidTabBar() -> some View {
        modifier(SolidTabBar())
    }

    func solidTopBarGround() -> some View {
        modifier(SolidTopBarGround())
    }
}

// The hard scroll edge still shows a blur of what sits under the bar; a solid surface hides it.
private struct SolidTopBarGround: ViewModifier {
    @Environment(\.phoneAppearance) private var appearance

    func body(content: Content) -> some View {
        if let appearance {
            ObservedTopBarGround(content: content, appearance: appearance)
        } else {
            SolidTopBarGroundBody(content: content, config: .defaults)
        }
    }
}

private struct ObservedTopBarGround<Content: View>: View {
    let content: Content
    @ObservedObject var appearance: PhoneAppearance

    var body: some View { SolidTopBarGroundBody(content: content, config: appearance.config) }
}

private struct SolidTopBarGroundBody<Content: View>: View {
    let content: Content
    let config: UIConfig
    @Environment(\.colorScheme) private var colorScheme
    @Environment(\.accessibilityReduceTransparency) private var reduceTransparency

    var body: some View {
        content.background {
            if config.solidSurfaces(dark: colorScheme == .dark, reduceTransparency: reduceTransparency) {
                Rectangle().fill(PhonePalette.ground).ignoresSafeArea(edges: .top)
            }
        }
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
