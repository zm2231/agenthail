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

        reported = nil
        probe.post(force: true)
        probe.removeFromSuperview()
        settle()
        if reported != nil {
            print("FAIL: a report queued before the probe left its window was delivered: \(String(describing: reported))")
            failures += 1
        }

        if failures > 0 { exit(1) }
        print("window rect reader: a probe follows its ancestors' moves and stops reporting once it leaves the window")
    }
}
