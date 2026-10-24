import AppKit

enum DesktopGlass {
    static func roles(_ config: UIConfig, dark: Bool, increased: Bool) -> [AccentContrast.RGB] {
        let colors = TextColors(config)
        let appearance = NSAppearance(named: dark ? .darkAqua : .aqua)!
        return colors.roles(dark: dark, increased: increased).map(AccentContrast.RGB.init(hex:))
            + [DesktopPalette.readableAccent(colors, in: appearance, increased: increased)]
    }

    static func floatingOpacity(_ config: UIConfig, dark: Bool, increaseContrast: Bool) -> Double {
        guard config.readability || increaseContrast else { return config.composerOpacity }
        let normal = config.readability ? solve(config, dark: dark, increased: false) : config.composerOpacity
        // Darker increased roles can need less glass; Increase Contrast never makes a surface more see-through.
        return increaseContrast ? max(normal, solve(config, dark: dark, increased: true)) : normal
    }

    private static func solve(_ config: UIConfig, dark: Bool, increased: Bool) -> Double {
        let polarity: ScrimField.Polarity = dark ? .dark : .light
        let target: Float = increased ? 7 : Float(config.contrastTarget)
        let luminances = roles(config, dark: dark, increased: increased).map { ScrimField.luminance(Float($0.r), Float($0.g), Float($0.b)) }
        let boundary = ScrimField.boundary(roles: luminances, polarity: polarity, target: target)
        let window = GlassSolve.luminance(dark ? PaletteTokens.window.dark : PaletteTokens.window.light)
        return max(config.composerOpacity, Double(GlassSolve.opacity(under: nil, polarity: polarity, window: window, boundary: boundary)))
    }
}
