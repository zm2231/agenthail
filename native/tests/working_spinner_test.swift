@testable import AgenthailHelpers
import AppKit
import Foundation
import SwiftUI

final class CountingHost<Content: View>: NSHostingView<Content> {
    var layouts = 0

    override func layout() {
        layouts += 1
        super.layout()
    }
}

struct SwiftUISymbolEffect: View {
    var body: some View {
        Image(systemName: "progress.indicator")
            .symbolEffect(.variableColor.iterative, options: .repeating)
    }
}

@main
struct WorkingSpinnerTest {
    @MainActor
    static func layouts<Content: View>(_ content: Content) -> Int {
        let host = CountingHost(rootView: content)
        host.frame = NSRect(x: 0, y: 0, width: 40, height: 40)
        let window = NSWindow(contentRect: host.frame, styleMask: .borderless, backing: .buffered, defer: false)
        window.contentView = host
        RunLoop.main.run(until: Date().addingTimeInterval(0.3))
        host.layouts = 0
        RunLoop.main.run(until: Date().addingTimeInterval(1))
        return withExtendedLifetime(window) { host.layouts }
    }

    @MainActor
    static func main() {
        let control = layouts(SwiftUISymbolEffect())
        let spinner = layouts(WorkingSpinner())
        print("hosting-view layouts in 1 s: SwiftUI symbol effect \(control), WorkingSpinner \(spinner)")
        check(control >= 20, "the probe sees a SwiftUI repeating symbol effect re-lay out its host every frame: \(control)")
        check(spinner <= 2, "the working spinner animates without re-laying out its SwiftUI host: \(spinner)")

        let view = WorkingSymbolView()
        check(view.image != nil, "the spinner shows the progress indicator symbol")
        view.tint = .systemRed
        check(view.contentTintColor == .systemRed, "a new tint reaches the image")
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
