@testable import AgenthailHelpers
import AppKit

final class Key {}

@main
struct TranscriptScrimHostTest {
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
        let window = NSWindow(contentRect: CGRect(x: -20000, y: -20000, width: 400, height: 300), styleMask: [.borderless], backing: .buffered, defer: false)
        let container = NSView(frame: CGRect(x: 0, y: 0, width: 400, height: 300))
        window.contentView = container
        let anchor = ScrimAnchor()
        let host = BackdropLayerView(frame: container.bounds)
        let header = BackdropLayerView(frame: CGRect(x: 0, y: 260, width: 400, height: 40))
        container.addSubview(host)
        container.addSubview(header)
        host.apply(style: BackdropLayerStyle(kind: .surface, role: .content, hostsAnchor: true), set: nil, anchor: anchor)
        header.apply(style: BackdropLayerStyle(kind: .surface, role: .content), set: nil, anchor: anchor)

        let scrim = anchor.transcript
        let key = Key()
        scrim.move(to: CGPoint(x: 0, y: 0))
        scrim.update(ObjectIdentifier(key), CGRect(x: 20, y: 20, width: 200, height: 40))
        scrim.flush()
        func hostMask() -> CALayer? { host.layer?.sublayers?.first?.sublayers?.first?.sublayers?.dropFirst(2).first?.mask }
        guard let rows = anchor.rows else {
            fatalError("the transcript made no row layers")
        }
        check(hostMask()?.sublayers?.contains { $0 === rows.root } == true, "the host's scrim mask holds the transcript rows")
        header.place()
        check(hostMask()?.sublayers?.contains { $0 === rows.root } == true, "another surface on the same anchor leaves the host's rows in place")
        host.place()
        check(hostMask()?.sublayers?.contains { $0 === rows.root } == true, "the host keeps its rows when it places again")

        let replacement = BackdropLayerView(frame: CGRect(x: 100, y: 0, width: 300, height: 300))
        container.addSubview(replacement)
        replacement.apply(style: BackdropLayerStyle(kind: .surface, role: .content, hostsAnchor: true), set: nil, anchor: anchor)
        func mask(_ view: BackdropLayerView) -> CALayer? { view.layer?.sublayers?.first?.sublayers?.first?.sublayers?.dropFirst(2).first?.mask }
        check(mask(replacement)?.sublayers?.contains { $0 === rows.root } == true, "a replacement host takes the transcript rows")
        host.place()
        host.layout()
        check(mask(replacement)?.sublayers?.contains { $0 === rows.root } == true, "the outgoing host placing again leaves the rows with the replacement")
        check(hostMask()?.sublayers?.contains { $0 === rows.root } != true, "the outgoing host no longer holds the rows")
        rows.position()
        check(rows.root.sublayers?.first?.frame.minX == -100, "the rows are positioned in the replacement's box, got \(String(describing: rows.root.sublayers?.first?.frame.minX))")

        let detachedAnchor = ScrimAnchor()
        let detached = BackdropLayerView(frame: container.bounds)
        detached.apply(style: BackdropLayerStyle(kind: .surface, role: .content, hostsAnchor: true), set: nil, anchor: detachedAnchor)
        let detachedScrim = detachedAnchor.transcript
        detachedScrim.move(to: .zero)
        detachedScrim.update(ObjectIdentifier(key), CGRect(x: 20, y: 20, width: 200, height: 40))
        detachedScrim.reportEnd(120)
        check(detachedScrim.mask.layers.isEmpty, "rows reported off-window wait for the host")
        container.addSubview(detached)
        check(detachedScrim.mask.layers.count == 2, "a host joining a window rebuilds the panels it was waiting on, got \(detachedScrim.mask.layers.count) layers")

        if failures > 0 { fatalError("\(failures) failures") }
        print("ok")
    }
}
