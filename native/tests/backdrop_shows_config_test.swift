@testable import AgenthailHelpers
import AppKit

@main
struct BackdropShowsConfigTest {
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
        func mesh(_ last: String) -> UIConfig {
            var config = UIConfig()
            config.values["background"] = .text("mesh: 2x2, #F0E0D0, #203040, #E0F0C0, \(last)")
            return config
        }
        func waitForRender(_ backdrop: WindowBackdrop, _ config: UIConfig) {
            let deadline = Date().addingTimeInterval(60)
            while !backdrop.shows(config), Date() < deadline {
                RunLoop.main.run(until: Date().addingTimeInterval(0.02))
            }
        }

        let first = mesh("#401020"), second = mesh("#102040")
        let backdrop = WindowBackdrop()
        backdrop.configure(first, wallpapers: [:])
        // A small window keeps each render cheap when every helper test runs at once.
        let window = NSWindow(contentRect: CGRect(x: -20000, y: -20000, width: 96, height: 48), styleMask: [.borderless], backing: .buffered, defer: false)
        window.contentView = NSView(frame: CGRect(x: 0, y: 0, width: 96, height: 48))
        window.appearance = NSAppearance(named: .darkAqua)
        backdrop.windowChanged(window)
        check(!backdrop.shows(first), "a backdrop that has not rendered does not show its config")
        waitForRender(backdrop, first)
        check(backdrop.shows(first), "the rendered backdrop shows its config")

        check(backdrop.current, "the backdrop is current for the config it was given")
        check(!backdrop.shows(second), "a backdrop not yet given the new config does not show it")

        backdrop.configure(second, wallpapers: [:])
        check(!backdrop.shows(second), "a newly given config shows only once rendered")
        waitForRender(backdrop, second)
        check(backdrop.shows(second), "the backdrop shows the new config after rendering")

        var plain = UIConfig()
        plain.values["background"] = .text("none")
        backdrop.configure(plain, wallpapers: [:])
        check(backdrop.shows(plain), "a config without a background has nothing to wait for")

        if failures > 0 { exit(1) }
        print("ok")
    }
}
