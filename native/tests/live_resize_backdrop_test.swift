@testable import AgenthailHelpers
import AppKit
import Combine

final class ResizingWindow: NSWindow {
    var resizing = false
    override var inLiveResize: Bool { resizing }
}

@main
struct LiveResizeBackdropTest {
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

        var config = UIConfig()
        config.values["background"] = .text("mesh: 2x2, #0000FF, #0000FF, #0000FF, #0000FF")
        let backdrop = WindowBackdrop()
        backdrop.configure(config, wallpapers: [:])
        let window = ResizingWindow(contentRect: NSRect(x: 0, y: 0, width: 240, height: 180), styleMask: [.titled, .resizable], backing: .buffered, defer: false)
        let scale = window.backingScaleFactor

        backdrop.windowChanged(window)
        wait { backdrop.image?.size == CGSize(width: 240, height: 180) }
        check(backdrop.image?.size == CGSize(width: 240, height: 180), "the first render matches the window, got \(String(describing: backdrop.image?.size))")
        check(backdrop.image.map { CGFloat($0.image.width) } == 240 * scale, "a settled window renders at full resolution")

        window.resizing = true
        for step in 1...30 {
            window.setContentSize(NSSize(width: 240 - CGFloat(step) * 2, height: 180 - CGFloat(step)))
            backdrop.windowChanged(window)
            RunLoop.main.run(until: Date().addingTimeInterval(0.005))
        }
        let dragged = CGSize(width: 180, height: 150)
        wait { backdrop.image?.size == dragged }
        check(backdrop.image?.size == dragged, "during a resize the backdrop catches up to the window's size, got \(String(describing: backdrop.image?.size))")
        check(backdrop.image.map { CGFloat($0.image.width) } == dragged.width, "during a resize the backdrop renders one point per pixel, got \(String(describing: backdrop.image?.image.width))")

        window.resizing = false
        backdrop.windowChanged(window)
        wait { backdrop.image.map { CGFloat($0.image.width) } == dragged.width * scale }
        check(backdrop.image.map { CGFloat($0.image.width) } == dragged.width * scale || scale == 1, "after the resize the backdrop renders at full resolution, got \(String(describing: backdrop.image?.image.width))")

        func hue(_ image: BackdropImage) -> String {
            var pixel = [UInt8](repeating: 0, count: 4)
            let context = CGContext(data: &pixel, width: 1, height: 1, bitsPerComponent: 8, bytesPerRow: 4, space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
            context.draw(image.image, in: CGRect(x: -CGFloat(image.image.width) / 2, y: -CGFloat(image.image.height) / 2, width: CGFloat(image.image.width), height: CGFloat(image.image.height)))
            return pixel[0] > pixel[2] ? "red" : "blue"
        }
        func background(_ value: String) -> UIConfig {
            var config = UIConfig()
            config.values["background"] = .text(value)
            return config
        }
        var published: [String] = []
        let watch = backdrop.$image.dropFirst().sink { published.append($0.map(hue) ?? "none") }
        window.setContentSize(NSSize(width: 200, height: 160))
        backdrop.windowChanged(window)
        wait { backdrop.rendering }
        check(backdrop.rendering, "the resize starts a render")
        backdrop.configure(background("none"), wallpapers: [:])
        backdrop.configure(background("mesh: 2x2, #FF0000, #FF0000, #FF0000, #FF0000"), wallpapers: [:])
        wait { published.last == "red" }
        watch.cancel()
        let afterSwitch = published.drop { $0 != "none" }
        check(afterSwitch.first == "none" && afterSwitch.last == "red" && !afterSwitch.contains("blue"), "a render started before the background changed is never shown, got \(published)")

        if failures > 0 { exit(1) }
        print("live resize backdrop: the image follows the window during a resize and sharpens after it")
    }
}
