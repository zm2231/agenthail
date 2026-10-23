@testable import AgenthailHelpers
import AppKit

@main
struct WindowRectReaderTest {
    @MainActor
    static func main() {
        _ = NSApplication.shared
        let window = NSWindow(contentRect: CGRect(x: 0, y: 0, width: 800, height: 600), styleMask: [.titled], backing: .buffered, defer: false)
        let column = NSView(frame: CGRect(x: 100, y: 0, width: 300, height: 600))
        let row = NSView(frame: CGRect(x: 20, y: 400, width: 200, height: 40))
        let probe = WindowRectReader.RectProbe(frame: row.bounds)
        var reported: CGRect?
        probe.onChange = { rect, _ in reported = rect }
        row.addSubview(probe)
        column.addSubview(row)
        window.contentView!.addSubview(column)

        var failures = 0
        func settle() { RunLoop.main.run(until: Date().addingTimeInterval(0.1)) }
        settle()
        if reported?.minX != 120 {
            print("FAIL: first report is \(String(describing: reported)), want x 120")
            failures += 1
        }

        column.setFrameOrigin(CGPoint(x: 300, y: 0))
        settle()
        if reported?.minX != 320 {
            print("FAIL: after the column moved, the probe reports \(String(describing: reported)), want x 320")
            failures += 1
        }
        if probe.measure()?.minX != 320 {
            print("FAIL: measure() reads \(String(describing: probe.measure())), want x 320")
            failures += 1
        }

        let other = NSView(frame: CGRect(x: 500, y: 0, width: 200, height: 600))
        window.contentView!.addSubview(other)
        other.addSubview(row)
        settle()
        if reported?.minX != 520 {
            print("FAIL: after the row moved to another column, the probe reports \(String(describing: reported)), want x 520")
            failures += 1
        }
        other.setFrameOrigin(CGPoint(x: 550, y: 0))
        settle()
        if reported?.minX != 570 || probe.measure()?.minX != 570 {
            print("FAIL: after the new column moved, the probe reports \(String(describing: reported)) and measures \(String(describing: probe.measure())), want x 570")
            failures += 1
        }
        column.setFrameOrigin(CGPoint(x: 0, y: 0))
        settle()
        if reported?.minX != 570 {
            print("FAIL: the old column moving reached the probe, which reports \(String(describing: reported)), want x 570")
            failures += 1
        }

        var left = 0
        probe.onLeave = { left += 1 }
        reported = nil
        probe.post(force: true)
        probe.removeFromSuperview()
        settle()
        if reported != nil {
            print("FAIL: a report queued before the probe left its window was delivered: \(String(describing: reported))")
            failures += 1
        }
        if left != 1 {
            print("FAIL: leaving the window called onLeave \(left) times, want 1")
            failures += 1
        }

        let backdrop = WindowBackdrop()
        let rect = CGRect(x: 0, y: 0, width: 10, height: 10)
        func rects(_ id: String) -> [CGRect] { backdrop.regions.values.filter { $0.id == id }.map(\.rect) }

        let outgoing = RegionHandle(id: "swap", region: .text, backdrop: backdrop)
        let incoming = RegionHandle(id: "swap", region: .text, backdrop: backdrop)
        outgoing.report(rect)
        incoming.report(rect.offsetBy(dx: 5, dy: 0))
        outgoing.report(rect)
        outgoing.leave()
        if rects("swap") != [rect.offsetBy(dx: 5, dy: 0)] {
            print("FAIL: the view that replaced another lost or shared the region when the old one left, got \(rects("swap"))")
            failures += 1
        }

        let before = RegionHandle(id: "order", region: .text, backdrop: backdrop)
        let after = RegionHandle(id: "order", region: .text, backdrop: backdrop)
        before.report(rect)
        before.leave()
        after.report(rect.offsetBy(dx: 7, dy: 0))
        if rects("order") != [rect.offsetBy(dx: 7, dy: 0)] {
            print("FAIL: a view that left before its replacement appeared still affects the region, got \(rects("order"))")
            failures += 1
        }

        if failures > 0 { exit(1) }
        print("window rect reader: a probe follows its ancestors' moves, and a region lasts exactly as long as its reader is in the window")
    }
}
