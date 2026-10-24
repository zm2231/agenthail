import AppKit
import SwiftUI
@testable import AgenthailHelpers

private final class Model: ObservableObject {
    @Published var colors = TextColors(accentChoice: 0x0000FF)
}

private struct Swatches: View {
    var body: some View {
        HStack(spacing: 0) {
            Rectangle().fill(DesktopPalette.text).frame(width: 20, height: 20)
            Rectangle().fill(DesktopPalette.accent).frame(width: 20, height: 20)
        }
    }
}

private struct Skipped: View, Equatable {
    static func == (_: Skipped, _: Skipped) -> Bool { true }
    var body: some View { Swatches() }
}

private struct Root: View {
    @ObservedObject var model: Model
    var body: some View {
        Skipped().equatable()
            .environment(\.textColors, model.colors)
            .environment(\.colorScheme, .dark)
    }
}

@main
struct TextColorsRepaintTest {
    @MainActor
    static func main() {
        _ = NSApplication.shared
        let model = Model()
        let host = NSHostingView(rootView: Root(model: model))
        host.frame = NSRect(x: 0, y: 0, width: 40, height: 20)
        let window = NSWindow(contentRect: host.frame, styleMask: .borderless, backing: .buffered, defer: false)
        window.contentView = host

        precondition(abs(red(host, x: 10) - 0xEC) <= 2, "body starts at the built-in dark-mode body colour")
        precondition(red(host, x: 30) < 5, "accent starts blue")

        model.colors.served["body"] = ServedColor(dark: "#000000", light: "#000000")
        settle()
        precondition(red(host, x: 10) < 5, "a served body colour repaints a leaf under a skipped parent")

        model.colors.accentChoice = 0xFF0000
        settle()
        precondition(red(host, x: 30) > 200, "a new accent repaints a leaf under a skipped parent")
        print("ok")
    }

    @MainActor
    private static func settle() {
        RunLoop.main.run(until: Date().addingTimeInterval(0.3))
    }

    @MainActor
    private static func red(_ view: NSView, x: Int) -> Int {
        view.layoutSubtreeIfNeeded()
        guard let rep = view.bitmapImageRepForCachingDisplay(in: view.bounds) else { return -1 }
        view.cacheDisplay(in: view.bounds, to: rep)
        let scale = rep.pixelsWide / Int(view.bounds.width)
        return Int(((rep.colorAt(x: x * scale, y: 10 * scale)?.usingColorSpace(.sRGB)?.redComponent) ?? -1) * 255)
    }
}
