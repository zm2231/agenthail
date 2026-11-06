@testable import AgenthailHelpers
import AppKit
import SwiftUI

@MainActor
final class PaneModel: ObservableObject {
    @Published var title = "Header"
}

struct PaneTree: View {
    @ObservedObject var model: PaneModel
    let backdrop: WindowBackdrop
    let window: ScrimAnchor

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                ForEach(0..<3, id: \.self) { index in
                    Text("Message \(index)").frame(height: 40).transcriptScrimRow()
                }
            }
            .frame(maxWidth: .infinity)
            .coordinateSpace(.named(TranscriptRows.contentSpace))
            .transcriptScrimContent()
            .transcriptScrimOrigin()
        }
        .clipped()
        .transcriptScrimSource()
        .safeAreaInset(edge: .top, spacing: 0) {
            Text(model.title).frame(maxWidth: .infinity).frame(height: 30).scrimText().backdropSurface(.black)
        }
        .safeAreaInset(edge: .bottom, spacing: 0) {
            Text("Composer").frame(maxWidth: .infinity).frame(height: 30).backdropSurface(.black)
        }
        .glassPane(.content, fallback: .black)
        .environment(\.windowBackdrop, backdrop)
        .environment(\.backdropPlace, BackdropPlace(role: nil, anchor: window))
    }
}

@main
struct TranscriptScrimPaneTest {
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
        let model = PaneModel()
        let hosting = NSHostingView(rootView: PaneTree(model: model, backdrop: backdrop, window: ScrimAnchor()))
        let window = NSWindow(contentRect: CGRect(x: -20000, y: -20000, width: 480, height: 320), styleMask: [.borderless], backing: .buffered, defer: false)
        window.contentView = hosting
        window.appearance = NSAppearance(named: .darkAqua)
        backdrop.windowChanged(window)

        func all<V: NSView>(_ type: V.Type, in view: NSView) -> [V] {
            (view as? V).map { [$0] } ?? [] + view.subviews.flatMap { all(type, in: $0) }
        }
        func pane() -> BackdropLayerView? { all(BackdropLayerView.self, in: hosting).first { $0.style.hostsAnchor } }
        func mask() -> CALayer? { pane()?.layer?.sublayers?.first?.sublayers?.first?.sublayers?.dropFirst(2).first?.mask }
        func held() -> Bool {
            guard let root = pane()?.anchor?.rows?.root else { return false }
            return mask()?.sublayers?.contains { $0 === root } == true
        }

        let deadline = Date().addingTimeInterval(60)
        while backdrop.layers == nil || !(pane()?.anchor?.rows?.active ?? false), Date() < deadline {
            RunLoop.main.run(until: Date().addingTimeInterval(0.02))
            hosting.layoutSubtreeIfNeeded()
        }
        let surfaces = all(BackdropLayerView.self, in: hosting).filter { $0.anchor === pane()?.anchor && !$0.style.hostsAnchor }
        check(surfaces.count >= 2, "the header and composer share the pane's anchor, got \(surfaces.count) other surfaces")
        check(pane()?.anchor?.rows?.active == true, "the transcript reported its rows to the pane, layers \(backdrop.layers != nil) active \(backdrop.active) pane \(pane() != nil) rows \(pane()?.anchor?.rows != nil) surfaces \(all(BackdropLayerView.self, in: hosting).count)")
        check(held(), "after the first layout the pane's scrim mask holds the transcript rows")

        model.title = "A longer header title"
        hosting.layoutSubtreeIfNeeded()
        for surface in surfaces { surface.place() }
        check(held(), "after the header and composer place again the pane's scrim mask still holds the transcript rows")

        if failures > 0 { exit(1) }
        print("transcript scrim pane: the pane keeps its transcript rows while the header and composer share its anchor")
    }
}
