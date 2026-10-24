@testable import AgenthailHelpers
import AppKit

@main
struct WindowButtonsTest {
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

        let origins = WindowButtons.origins(center: CGPoint(x: 12, y: 30), size: CGSize(width: 14, height: 14), count: 3)
        check(origins == [CGPoint(x: 12, y: 23), CGPoint(x: 32, y: 23), CGPoint(x: 52, y: 23)], "the buttons step by the spacing and centre on the band, got \(origins)")
        check(WindowButtons.trailing == WindowButtons.paneInset + WindowButtons.span + 12, "the header starts after the buttons")
        check(WindowButtons.headerInset(toggle: true) == WindowButtons.trailing + WindowButtons.toggleWidth + 12, "the header starts after the sidebar toggle")

        let window = NSWindow(contentRect: CGRect(x: -20000, y: -20000, width: 600, height: 400), styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView], backing: .buffered, defer: false)
        window.titlebarAppearsTransparent = true
        let height = window.contentView!.bounds.height
        for (top, leading) in [(CGFloat(12), WindowButtons.paneInset), (0, WindowButtons.paneInset), (0, WindowButtons.stripInset)] {
            let band = CGRect(x: top, y: height - top - WindowButtons.bandHeight, width: 300, height: WindowButtons.bandHeight)
            WindowButtons.place(in: window, band: band, leading: leading)
            for (index, kind) in WindowButtons.kinds.enumerated() {
                guard let button = window.standardWindowButton(kind), let container = button.superview else {
                    check(false, "the window has its \(kind) button")
                    continue
                }
                let frame = container.convert(button.frame, to: nil)
                check(abs(frame.midY - band.midY) < 0.5, "button \(index) centres on a band \(top) pt down, got \(frame.midY) for \(band.midY)")
                check(abs(frame.minX - (band.minX + leading + CGFloat(index) * WindowButtons.spacing)) < 0.5, "button \(index) starts \(leading) pt into the band, got \(frame.minX)")
            }
        }

        let band = WindowButtonBandView(frame: CGRect(x: 0, y: height - WindowButtons.bandHeight, width: 300, height: WindowButtons.bandHeight))
        window.contentView!.addSubview(band)
        RunLoop.main.run(until: Date().addingTimeInterval(0.1))
        if let close = window.standardWindowButton(.closeButton), let container = close.superview {
            let placed = close.frame.origin
            close.setFrameOrigin(.zero)
            RunLoop.main.run(until: Date().addingTimeInterval(0.1))
            check(close.frame.origin == placed, "a button AppKit moves goes back to the band, got \(close.frame.origin) for \(placed)")
            check(abs(container.convert(close.frame, to: nil).midY - (height - WindowButtons.bandHeight / 2)) < 0.5, "the band view centres the buttons on itself")
        }

        if failures > 0 { exit(1) }
        print("window buttons: centred in the title band at its leading inset, wherever the band sits")
    }
}
