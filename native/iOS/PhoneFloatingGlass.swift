import SwiftUI

extension EnvironmentValues {
    @Entry var backdropKey: PhoneAppearance.Key? = nil
}

extension PhoneAppearance {
    func floatingOpacity(_ key: Key) -> Double {
        guard key.readable else { return config.composerOpacity }
        let solved = floatingOpacity(key, target: key.target)
        guard key.target >= Self.increasedTarget else { return solved }
        let normal = config.readability ? floatingOpacity(key, target: Self.contrastTarget(config, increased: false)) : config.composerOpacity
        // Darker increased roles can need less glass; Increase Contrast never makes a surface more see-through.
        return max(solved, normal)
    }

    private func floatingOpacity(_ key: Key, target: Float) -> Double {
        let polarity: ScrimField.Polarity = key.dark ? .dark : .light
        let roles = PhonePalette.textRoles(TextColors(config), dark: key.dark, increased: target >= Self.increasedTarget)
        let boundary = ScrimField.boundary(roles: roles.map { ScrimField.luminance($0.x, $0.y, $0.z) }, polarity: polarity, target: target)
        let window = GlassSolve.luminance(key.dark ? PaletteTokens.window.dark : PaletteTokens.window.light)
        let shown = active ? extremes[key] : nil
        return max(config.composerOpacity, Double(GlassSolve.opacity(under: shown, polarity: polarity, window: window, boundary: boundary)))
    }
}

struct FloatingGlass<S: Shape>: ViewModifier {
    let shape: S
    let interactive: Bool
    @Environment(\.phoneAppearance) private var appearance
    @Environment(\.accessibilityReduceTransparency) private var reduceTransparency

    func body(content: Content) -> some View {
        if let appearance {
            FloatingGlassBody(content: content, shape: shape, interactive: interactive, appearance: appearance)
        } else {
            FloatingGlassLayers(content: content, shape: shape, interactive: interactive, solid: reduceTransparency, opacity: UIConfig.defaults.composerOpacity)
        }
    }
}

private struct FloatingGlassBody<Content: View, S: Shape>: View {
    let content: Content
    let shape: S
    let interactive: Bool
    @ObservedObject var appearance: PhoneAppearance
    @Environment(\.backdropKey) private var backdropKey
    @Environment(\.colorScheme) private var colorScheme
    @Environment(\.colorSchemeContrast) private var contrast
    @Environment(\.accessibilityReduceTransparency) private var reduceTransparency

    private var key: PhoneAppearance.Key {
        backdropKey ?? PhoneAppearance.Key(surface: .pane, size: .zero, scale: 1, dark: colorScheme == .dark, solid: false,
                                           target: PhoneAppearance.contrastTarget(appearance.config, increased: contrast == .increased),
                                           readable: appearance.config.readability || contrast == .increased)
    }

    var body: some View {
        let solid = appearance.config.solidSurfaces(dark: colorScheme == .dark, reduceTransparency: reduceTransparency)
        FloatingGlassLayers(content: content, shape: shape, interactive: interactive, solid: solid, opacity: solid ? 1 : appearance.floatingOpacity(key))
    }
}

private struct FloatingGlassLayers<Content: View, S: Shape>: View {
    let content: Content
    let shape: S
    let interactive: Bool
    let solid: Bool
    let opacity: Double

    var body: some View {
        let tint = content
            .background(PhonePalette.ground.opacity(solid ? 1 : opacity), in: shape)
            .overlay { if solid { shape.stroke(PhonePalette.hairline) } }
            .shadow(color: .black.opacity(solid ? 0.12 : 0), radius: solid ? 12 : 0, y: solid ? 4 : 0)
        if #available(iOS 26, *) {
            tint.glassEffect(solid ? .identity : interactive ? .regular.interactive() : .regular, in: shape)
        } else {
            tint.background { if !solid { shape.fill(.regularMaterial) } }
        }
    }
}
