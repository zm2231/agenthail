@testable import AgenthailHelpers
import AppKit
import SwiftUI

@MainActor
final class LayoutModel: ObservableObject {
    @Published var offset: CGFloat = 20
    @Published var inspector: CGFloat = 80
    @Published var session = "a"
}

struct LayoutTree: View {
    @ObservedObject var model: LayoutModel
    let backdrop: WindowBackdrop
    let window: ScrimAnchor

    var body: some View {
        HStack(spacing: 0) {
            VStack(alignment: .leading, spacing: 0) {
                HStack(spacing: 0) {
                    Color.clear.frame(width: model.offset, height: 1)
                    Text(model.session == "a" ? "First session" : "Second session, longer title")
                        .frame(width: 120, height: 30)
                        .scrimText()
                        .id(model.session)
                }
                Spacer()
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .glassPane(.content, fallback: .black)
            Color.clear.frame(width: model.inspector)
        }
        .environment(\.windowBackdrop, backdrop)
        .environment(\.backdropPlace, BackdropPlace(role: nil, anchor: window))
    }
}

@main
struct BackdropFollowsLayoutTest {
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

        var config = UIConfig()
        config.values["background"] = .text("mesh: 2x2, #F0E0D0, #203040, #E0F0C0, #401020")
        let backdrop = WindowBackdrop()
        backdrop.configure(config, wallpapers: [:])
        let model = LayoutModel()
        let hosting = NSHostingView(rootView: LayoutTree(model: model, backdrop: backdrop, window: ScrimAnchor()))
        let window = NSWindow(contentRect: CGRect(x: -20000, y: -20000, width: 480, height: 240), styleMask: [.borderless], backing: .buffered, defer: false)
        window.contentView = hosting
        window.appearance = NSAppearance(named: .darkAqua)
        backdrop.windowChanged(window)
        let deadline = Date().addingTimeInterval(30)
        while backdrop.layers == nil && Date() < deadline { RunLoop.main.run(until: Date().addingTimeInterval(0.02)) }
        hosting.layoutSubtreeIfNeeded()
        RunLoop.main.run(until: Date().addingTimeInterval(0.1))

        func all<V: NSView>(_ type: V.Type, in view: NSView) -> [V] {
            (view as? V).map { [$0] } ?? [] + view.subviews.flatMap { all(type, in: $0) }
        }
        func region() -> CGRect? {
            all(ScrimRegionView.self, in: hosting).first.map { $0.convert($0.bounds, to: nil) }
        }
        func pane() -> BackdropLayerView? {
            all(BackdropLayerView.self, in: hosting).first { $0.style.hostsAnchor }
        }
        func coverage(_ rect: CGRect) -> Float? {
            guard let mask = pane()?.anchor?.mask else { return nil }
            let local = CGRect(x: rect.minX - mask.rect.minX, y: mask.rect.maxY - rect.maxY, width: rect.width, height: rect.height)
            let x0 = Int(local.minX / ScrimMask.cell), x1 = Int((local.maxX / ScrimMask.cell).rounded(.up))
            let y0 = Int(local.minY / ScrimMask.cell), y1 = Int((local.maxY / ScrimMask.cell).rounded(.up))
            var minimum: Float = 1
            let values = maskValues(mask.image)
            for y in max(y0, 0)..<min(y1, mask.image.height) { for x in max(x0, 0)..<min(x1, mask.image.width) { minimum = min(minimum, values[y * mask.image.width + x]) } }
            return minimum
        }
        func baseFrame() -> CGRect? {
            guard let view = pane(), let box = view.layer?.sublayers?.first?.sublayers?.first, let base = box.sublayers?.first else { return nil }
            return view.convert(view.layer!.convert(base.frame, from: box), to: nil)
        }
        func settle(_ change: () -> Void) {
            change()
            hosting.layoutSubtreeIfNeeded()
        }

        check(region() != nil && coverage(region()!) == 1, "the first layout scrims the region, got \(String(describing: region().flatMap(coverage)))")
        check(baseFrame() == window.contentView.map { $0.convert($0.bounds, to: nil) }, "the pane's background sits at the window's origin, got \(String(describing: baseFrame()))")

        let before = region()!
        settle { model.offset = 200 }
        let moved = region()!
        check(moved.minX != before.minX, "the region moved, \(before) to \(moved)")
        check(coverage(moved) == 1, "in the same layout pass the scrim covers the moved region, got \(String(describing: coverage(moved)))")
        check((coverage(before) ?? 1) < 0.5, "in the same layout pass the old place is no longer scrimmed, got \(String(describing: coverage(before)))")

        settle {
            model.offset = 40
            model.inspector = 220
        }
        check(coverage(region()!) == 1, "after the inspector widens, the same pass scrims the region, got \(String(describing: coverage(region()!)))")
        let paneRect = pane().map { $0.convert($0.bounds, to: nil) }
        check(paneRect?.width == 480 - 220 - backdrop.gutter && pane()?.anchor?.area == paneRect, "the pane's scrim area follows its new width, got \(String(describing: pane()?.anchor?.area)) for \(String(describing: paneRect))")
        check(baseFrame() == window.contentView.map { $0.convert($0.bounds, to: nil) }, "the background stays at the window's origin as the pane resizes, got \(String(describing: baseFrame()))")

        settle { model.session = "b" }
        check(coverage(region()!) == 1, "a replaced region is scrimmed in the same pass, got \(String(describing: coverage(region()!)))")

        let peak = backdrop.layers?.layers.panes[.content]?.scrimPeak ?? 0
        settle { window.setContentSize(NSSize(width: 520, height: 260)) }
        let scrim = pane()?.layer?.sublayers?.first?.sublayers?.first?.sublayers?[2]
        let flat = scrim?.sublayers?[1]
        let image = scrim?.sublayers?[0]
        check(flat?.isHidden == false && flat?.opacity == peak && image?.isHidden == true, "before the layers match a new window size the region draws the flat peak scrim, got flat hidden \(String(describing: flat?.isHidden)) opacity \(String(describing: flat?.opacity)) peak \(peak)")

        if failures > 0 { exit(1) }
        print("backdrop follows layout: moves, resizes and replacements are scrimmed in the layout pass that makes them, and a stale size draws the flat peak")
    }

    static func maskValues(_ image: CGImage) -> [Float] {
        let w = image.width, h = image.height
        var raw = [UInt8](repeating: 0, count: w * h * 4)
        let ctx = CGContext(data: &raw, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4, space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
        ctx.draw(image, in: CGRect(x: 0, y: 0, width: w, height: h))
        return (0..<(w * h)).map { Float(raw[$0 * 4 + 3]) / 255 }
    }
}
