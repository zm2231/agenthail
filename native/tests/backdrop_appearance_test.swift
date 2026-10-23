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
            let deadline = Date().addingTimeInterval(30)
            while !done() && Date() < deadline { RunLoop.main.run(until: Date().addingTimeInterval(0.02)) }
        }
        func hue(_ image: BackdropImage?) -> String {
            guard let image else { return "none" }
            var pixel = [UInt8](repeating: 0, count: 4)
            let context = CGContext(data: &pixel, width: 1, height: 1, bitsPerComponent: 8, bytesPerRow: 4, space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
            context.draw(image.image, in: CGRect(x: -CGFloat(image.image.width) / 2, y: -CGFloat(image.image.height) / 2, width: CGFloat(image.image.width), height: CGFloat(image.image.height)))
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
        wait { hue(backdrop.image) == "blue" }
        check(hue(backdrop.image) == "blue", "a dark window draws the dark background, got \(hue(backdrop.image))")

        window.appearance = NSAppearance(named: .aqua)
        backdrop.windowChanged(window)
        wait { hue(backdrop.image) == "red" }
        check(hue(backdrop.image) == "red", "switching the window to light draws the light background, got \(hue(backdrop.image))")

        var pictures = UIConfig()
        pictures.values["background"] = .text("image: art/a-dark.jpg")
        pictures.values["background.light"] = .text("image: art/a-light.jpg")
        let lightOnly = WallpaperImage(id: "art/a-light.jpg", image: solid(red: true))
        backdrop.configure(pictures, wallpapers: ["art/a-light.jpg": lightOnly])
        check(backdrop.active, "a light window with its picture loaded shows a backdrop")
        window.appearance = NSAppearance(named: .darkAqua)
        backdrop.windowChanged(window)
        check(!backdrop.active, "a dark window whose picture has not loaded shows no backdrop rather than the light one")

        var glass = UIConfig()
        glass.values["glass.composer.opacity"] = .number(0)
        check(WindowBackdrop.floatingOpacity(glass, increaseContrast: false) == 0.2, "readable floating glass never drops below the measured floor")
        glass.values["glass.composer.opacity"] = .number(0.6)
        check(WindowBackdrop.floatingOpacity(glass, increaseContrast: false) == 0.6, "a stronger setting is kept")
        glass.values["glass.composer.opacity"] = .number(0)
        check(WindowBackdrop.floatingOpacity(glass, increaseContrast: true) > 0.9, "Increase Contrast raises floating glass for its higher target")
        glass.values["glass.readability"] = .text("off")
        check(WindowBackdrop.floatingOpacity(glass, increaseContrast: false) == 0, "readability off uses the setting exactly")

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
