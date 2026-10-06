import AppKit
import SwiftUI

@MainActor
final class Measured {
    var dock: CGFloat = 0
}

struct Rows: View {
    let count: Int

    var body: some View {
        VStack(spacing: 0) {
            ForEach(0..<count, id: \.self) { Color.gray.frame(height: 30).overlay(Text("Fixture row \($0)")) }
        }
    }
}

enum Placement {
    case aboveTranscript
    case composerInset
}

struct Column: View {
    let rows: Int
    let placement: Placement
    let measured: Measured

    var body: some View {
        VStack(spacing: 0) {
            Color.clear.frame(height: 52)
            if placement == .aboveTranscript { dock }
            ScrollView { Text("Fixture transcript") }
                .safeAreaInset(edge: .bottom, spacing: 0) {
                    VStack(spacing: 0) {
                        if placement == .composerInset { dock }
                        Color.clear.frame(height: 100)
                    }
                }
        }
    }

    private var dock: some View {
        BoundedDock { Rows(count: rows) }
            .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { measured.dock = $0 }
    }
}

@main
struct BoundedDockTest {
    @MainActor
    static func main() {
        _ = NSApplication.shared
        let cap = BoundedDock<EmptyView>.defaultMaxHeight

        for placement in [Placement.composerInset, .aboveTranscript] {
            let few = layout(rows: 2, placement: placement)
            check(few.frame == 600, "\(placement): a short dock leaves the column at the window height")
            check(few.dock == 60, "\(placement): a short dock keeps its natural height, got \(few.dock)")

            let many = layout(rows: 40, placement: placement)
            check(many.frame == 600, "\(placement): a long dock never makes the column taller than the window, got \(many.frame)")
            check(many.minimum <= 52 + 100 + cap, "\(placement): a long dock never raises the window minimum height past the cap, got \(many.minimum)")
            check(many.dock == cap, "\(placement): a long dock scrolls at the cap, got \(many.dock)")
        }
    }

    @MainActor
    private static func layout(rows: Int, placement: Placement) -> (frame: CGFloat, minimum: CGFloat, dock: CGFloat) {
        let measured = Measured()
        let hosting = NSHostingView(rootView: Column(rows: rows, placement: placement, measured: measured))
        hosting.sizingOptions = [.minSize]
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 800, height: 600), styleMask: [.titled, .resizable], backing: .buffered, defer: false)
        window.contentView = hosting
        hosting.layoutSubtreeIfNeeded()
        RunLoop.main.run(until: Date().addingTimeInterval(0.05))
        return (hosting.frame.height, window.contentMinSize.height, measured.dock)
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
