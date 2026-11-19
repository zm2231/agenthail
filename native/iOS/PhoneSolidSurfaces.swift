import SwiftUI

extension View {
    func solidTabBar() -> some View {
        modifier(SolidTabBar())
    }

    func solidTopBarGround() -> some View {
        modifier(SolidTopBarGround())
    }
}

// A hard scroll edge still leaves text under the bar legible on a phone, so glass gets a mostly opaque ground.
private struct SolidTopBarGround: ViewModifier {
    @Environment(\.phoneAppearance) private var appearance

    func body(content: Content) -> some View {
        if let appearance {
            ObservedTopBarGround(content: content, appearance: appearance)
        } else {
            SolidTopBarGroundBody(content: content, config: .defaults) { _, _ in Color(uiColor: .systemBackground) }
        }
    }
}

private struct ObservedTopBarGround<Content: View>: View {
    let content: Content
    @ObservedObject var appearance: PhoneAppearance

    var body: some View { SolidTopBarGroundBody(content: content, config: appearance.config, pane: appearance.solidPaneColor) }
}

private struct SolidTopBarGroundBody<Content: View>: View {
    static var glassGround: Double { 0.86 }
    let content: Content
    let config: UIConfig
    let pane: (Bool, Float) -> Color
    @Environment(\.colorScheme) private var colorScheme
    @Environment(\.accessibilityReduceTransparency) private var reduceTransparency
    @Environment(\.colorSchemeContrast) private var contrast

    var body: some View {
        let dark = colorScheme == .dark
        let ground = pane(dark, PhoneAppearance.contrastTarget(config, increased: contrast == .increased))
        content.background {
            if config.solidSurfaces(dark: dark, reduceTransparency: reduceTransparency) {
                Rectangle().fill(ground).ignoresSafeArea(edges: .top)
            } else {
                Rectangle().fill(ground.opacity(Self.glassGround)).background(.ultraThinMaterial).ignoresSafeArea(edges: .top)
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
