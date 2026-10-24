@testable import AgenthailHelpers
import AppKit
import SwiftUI

@MainActor
final class Measured {
    var lead: CGFloat = 0
    var chosen: StatusDensity?
}

struct Strip: View {
    let lead: CGFloat
    let full: AnyView
    let measured: Measured

    var body: some View {
        StatusStripFit {
            Color.clear.frame(width: lead, height: 10)
                .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { measured.lead = $0 }
        } tail: { density in
            tier(density)
                .onGeometryChange(for: Bool.self) { _ in true } action: { _ in measured.chosen = density }
        }
    }

    @ViewBuilder
    private func tier(_ density: StatusDensity) -> some View {
        switch density {
        case .full: full
        case .compact: Color.clear.frame(width: 260, height: 10)
        case .icons: Color.clear.frame(width: 90, height: 10)
        case .none: Spacer(minLength: 0)
        }
    }
}

@main
struct StatusStripFitTest {
    @MainActor
    static func main() {
        _ = NSApplication.shared
        let wide = AnyView(Color.clear.frame(width: 420, height: 10))
        let cases: [(CGFloat, StatusDensity)] = [(900, .full), (500, .compact), (340, .icons), (200, .none)]
        for (width, expected) in cases {
            let result = layout(width: width, full: wide)
            check(result.chosen == expected, "at \(width) the strip shows \(expected), got \(String(describing: result.chosen))")
            check(result.lead == 180, "at \(width) the connection controls keep their width, got \(result.lead)")
            check(result.minimum <= 180 + DesktopSpace.l, "at \(width) the strip never raises the window minimum width, got \(result.minimum)")
        }

        let names = AnyView(Text(String(repeating: "remote-host · ", count: 200)).lineLimit(1))
        let long = layout(width: 900, full: names)
        check(long.chosen == .compact, "unbounded host names fall back instead of clipping, got \(String(describing: long.chosen))")
        check(long.lead == 180, "unbounded host names never squeeze the connection controls, got \(long.lead)")
    }

    @MainActor
    private static func layout(width: CGFloat, full: AnyView) -> (chosen: StatusDensity?, lead: CGFloat, minimum: CGFloat) {
        let measured = Measured()
        let hosting = NSHostingView(rootView: Strip(lead: 180, full: full, measured: measured))
        hosting.sizingOptions = [.minSize]
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: width, height: 26), styleMask: [.titled, .resizable], backing: .buffered, defer: false)
        window.contentView = hosting
        hosting.layoutSubtreeIfNeeded()
        RunLoop.main.run(until: Date().addingTimeInterval(0.05))
        return (measured.chosen, measured.lead, window.contentMinSize.width)
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
