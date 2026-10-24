@testable import AgenthailHelpers

@main
struct GlassSolveTest {
    static func main() {
        for dark in [true, false] {
            let polarity: ScrimField.Polarity = dark ? .dark : .light
            let window = GlassSolve.luminance(dark ? PaletteTokens.window.dark : PaletteTokens.window.light)
            let builtIn = TextColors.builtIn.roles(dark: dark, increased: false).map(GlassSolve.luminance)
            let boundary = ScrimField.boundary(roles: builtIn, polarity: polarity, target: 4.5)
            let floor = GlassSolve.opacity(under: nil, polarity: polarity, window: window, boundary: boundary)
            precondition(abs(floor - GlassSolve.measuredFloor) < 0.0001, "built-in roles at 4.5 keep the measured floor (dark \(dark)), got \(floor)")

            let unreachable = ScrimField.boundary(roles: builtIn, polarity: polarity, target: 7)
            precondition(GlassSolve.opacity(under: nil, polarity: polarity, window: window, boundary: unreachable) == 1, "a target the built-in roles cannot reach makes floating glass solid (dark \(dark))")

            let faint = edgeGrey(dark: dark, surface: dark ? PaletteTokens.raised.dark : PaletteTokens.raised.light)
            let fainter = ScrimField.boundary(roles: builtIn + [faint], polarity: polarity, target: 4.5)
            let glass = GlassSolve.level(polarity: polarity, window: window)
            for shown: Float in [0, 0.05, 0.25, 0.5, 1, glass] {
                let alpha = GlassSolve.opacity(under: shown, polarity: polarity, window: window, boundary: fainter)
                let worst = dark ? max(shown, glass) : min(shown, glass)
                let composite = alpha * window + (1 - alpha) * worst
                precondition(alpha == 1 || ScrimField.passes(composite, boundary: fainter + (dark ? 1e-5 : -1e-5), polarity: polarity),
                             "a faint custom colour reads on floating glass over luminance \(shown) (dark \(dark)): alpha \(alpha), shown \(composite), boundary \(fainter)")
                precondition(alpha >= floor, "a fainter role never lowers floating glass (dark \(dark))")
            }
            let bright = GlassSolve.opacity(under: dark ? 1 : 0, polarity: polarity, window: window, boundary: boundary)
            precondition(bright > floor, "art under floating glass raises it (dark \(dark))")

            let strongest = ScrimField.boundary(roles: [dark ? 1 : 0], polarity: polarity, target: 4.5)
            for shown: Float? in [nil, dark ? 0 : 1] {
                let alpha = GlassSolve.opacity(under: shown, polarity: polarity, window: window, boundary: strongest)
                precondition(alpha == GlassSolve.measuredFloor, "the strongest served text keeps the measured floor (dark \(dark), shown \(String(describing: shown))), got \(alpha)")
            }
        }
        print("ok")
    }

    static func edgeGrey(dark: Bool, surface: UInt32) -> Float {
        let ground = GlassSolve.luminance(surface)
        return dark ? 4.5 * (ground + 0.05) - 0.05 : (ground + 0.05) / 4.5 - 0.05
    }
}
