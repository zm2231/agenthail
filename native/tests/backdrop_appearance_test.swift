@testable import AgenthailHelpers
import AppKit

@main
struct BackdropAppearanceTest {
    @MainActor
    static func main() {
        _ = NSApplication.shared
        var failures = 0
        func check(_ condition: Bool, _ message: String) {
            if !condition {
                print("FAIL: \(message)")
                failures += 1
            }
        }
        func wait(until done: () -> Bool) {
            while !done() { RunLoop.main.run(until: Date().addingTimeInterval(0.02)) }
        }
        func hue(_ layers: BackdropLayerSet?) -> String {
            guard let image = layers?.layers.base else { return "none" }
            var pixel = [UInt8](repeating: 0, count: 4)
            let context = CGContext(data: &pixel, width: 1, height: 1, bitsPerComponent: 8, bytesPerRow: 4, space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
            context.draw(image, in: CGRect(x: -CGFloat(image.width) / 2, y: -CGFloat(image.height) / 2, width: CGFloat(image.width), height: CGFloat(image.height)))
            return pixel[0] > pixel[2] ? "red" : "blue"
        }

        var config = UIConfig()
        config.values["background"] = .text("mesh: 2x2, #0000FF, #0000FF, #0000FF, #0000FF")
        config.values["background.light"] = .text("mesh: 2x2, #FF0000, #FF0000, #FF0000, #FF0000")
        check(config.background(dark: true) != config.background(dark: false), "a preset has distinct dark and light backgrounds")

        var plain = UIConfig()
        plain.values["background"] = .text("image: backgrounds/a.png")
        check(plain.backgroundLight == .image(path: "backgrounds/a.png") && plain.imagePaths == ["backgrounds/a.png"], "without a light value the light background is the same image, loaded once")
        var art = UIConfig()
        art.values["background"] = .text("image: art/tide-dark.jpg")
        art.values["background.light"] = .text("image: art/tide-light.jpg")
        check(art.imagePaths == ["art/tide-dark.jpg", "art/tide-light.jpg"], "both pictures of a built-in are loaded, got \(art.imagePaths)")

        let backdrop = WindowBackdrop()
        backdrop.configure(config, wallpapers: [:])
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 200, height: 140), styleMask: [.titled], backing: .buffered, defer: false)
        window.appearance = NSAppearance(named: .darkAqua)
        backdrop.windowChanged(window)
        wait { hue(backdrop.layers) == "blue" }
        check(hue(backdrop.layers) == "blue", "a dark window draws the dark background, got \(hue(backdrop.layers))")
        check(!backdrop.solid && backdrop.tints[.chrome] ?? 1 < 1, "a dark window keeps translucent glass")

        window.appearance = NSAppearance(named: .aqua)
        backdrop.windowChanged(window)
        check(backdrop.layers == nil, "the dark layers are dropped as soon as the window turns light, so dark scrims never sit under dark text")
        wait { hue(backdrop.layers) == "red" }
        check(hue(backdrop.layers) == "red", "switching the window to light draws the light background, got \(hue(backdrop.layers))")
        check(backdrop.solid && [.sidebar, .content, .chrome].allSatisfy { backdrop.tints[$0] == 1 } && backdrop.composerOpacity == 1,
              "a light window draws every pane, strip and floating surface solid, got \(backdrop.tints) floating \(backdrop.composerOpacity)")
        check(backdrop.layers?.layers.scrim == nil && backdrop.layers?.layers.panes.values.allSatisfy({ $0.scrim == nil }) == true, "solid light surfaces need no scrim")

        var pictures = UIConfig()
        pictures.values["background"] = .text("image: art/a-dark.jpg")
        pictures.values["background.light"] = .text("image: art/a-light.jpg")
        let lightOnly = WallpaperImage(id: "art/a-light.jpg", image: solid(red: true))
        backdrop.configure(pictures, wallpapers: ["art/a-light.jpg": lightOnly])
        check(backdrop.active, "a light window with its picture loaded shows a backdrop")
        window.appearance = NSAppearance(named: .darkAqua)
        backdrop.windowChanged(window)
        check(!backdrop.active, "a dark window whose picture has not loaded shows no backdrop rather than the light one")

        let before = backdrop.generation
        NotificationCenter.default.post(name: NSColor.systemColorsDidChangeNotification, object: nil)
        wait { backdrop.generation > before }
        check(backdrop.generation > before, "a system colour change, such as a new accent, solves the backdrop again")

        var glass = UIConfig()
        glass.values["glass.composer.opacity"] = .number(0)
        for dark in [true, false] {
            glass.values["glass.composer.opacity"] = .number(0)
            glass.values["glass.readability"] = .text("auto")
            let base = DesktopGlass.floatingOpacity(glass, dark: dark, increaseContrast: false)
            check(base >= Double(GlassSolve.measuredFloor) - 0.0001, "readable floating glass never drops below the measured floor (dark \(dark)), got \(base)")
            glass.colors = ["muted": ServedColor(dark: "#77777c", light: "#85858b")]
            check(DesktopGlass.floatingOpacity(glass, dark: dark, increaseContrast: false) > base, "a fainter served role raises floating glass (dark \(dark))")
            glass.colors = nil
            glass.values["glass.composer.opacity"] = .number(0.99)
            check(DesktopGlass.floatingOpacity(glass, dark: dark, increaseContrast: false) == 0.99, "a stronger setting is kept (dark \(dark))")
            glass.values["glass.composer.opacity"] = .number(0)
            check(DesktopGlass.floatingOpacity(glass, dark: dark, increaseContrast: true) >= base, "Increase Contrast never makes floating glass more see-through (dark \(dark))")
            glass.values["glass.readability"] = .text("off")
            check(DesktopGlass.floatingOpacity(glass, dark: dark, increaseContrast: false) == 0, "readability off uses the setting exactly (dark \(dark))")
        }

        if failures > 0 { exit(1) }
        print("backdrop appearance: dark and light windows draw their own background")
    }

    static func solid(red: Bool) -> CGImage {
        let context = CGContext(data: nil, width: 4, height: 4, bitsPerComponent: 8, bytesPerRow: 16, space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
        context.setFillColor(red ? CGColor(red: 1, green: 0, blue: 0, alpha: 1) : CGColor(red: 0, green: 0, blue: 1, alpha: 1))
        context.fill(CGRect(x: 0, y: 0, width: 4, height: 4))
        return context.makeImage()!
    }
}
