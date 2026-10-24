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
    }
}

@main
struct TextColorsRepaintTest {
    @MainActor
    static func main() {
        _ = NSApplication.shared
        for dark in [true, false] {
            check(dark: dark)
        }
        print("ok")
    }

    @MainActor
    private static func check(dark: Bool) {
        let model = Model()
        let host = NSHostingView(rootView: Root(model: model))
        host.frame = NSRect(x: 0, y: 0, width: 40, height: 20)
        let window = NSWindow(contentRect: host.frame, styleMask: .borderless, backing: .buffered, defer: false)
        window.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
        window.colorSpace = .sRGB
        window.contentView = host

        let body = PaletteTokens.roles[.body]!
        let start = Int(((dark ? body.dark : body.light) >> 16) & 0xFF)
        expect(red(host, x: 10), near: start, "body starts at the built-in body colour (dark \(dark))")
        expect(red(host, x: 30), near: 0, "accent starts blue (dark \(dark))")

        let served = dark ? 0x00 : 0xFF
        let hex = String(format: "#%02x0000", served)
        model.colors.served["body"] = ServedColor(dark: hex, light: hex)
        settle()
        expect(red(host, x: 10), near: served, "a served body colour repaints a leaf under a skipped parent (dark \(dark))")

        model.colors.accentChoice = 0xFF0000
        settle()
        expect(red(host, x: 30), near: 0xFF, "a new accent repaints a leaf under a skipped parent (dark \(dark))")
    }

    private static func expect(_ red: Int, near wanted: Int, _ message: String) {
        precondition(abs(red - wanted) <= 2, "\(message): red \(red), wanted \(wanted)")
    }

    @MainActor
    private static func settle() {
        RunLoop.main.run(until: Date().addingTimeInterval(0.3))
    }

    @MainActor
    private static func red(_ view: NSView, x: Int) -> Int {
        view.layoutSubtreeIfNeeded()
        guard let rep = view.bitmapImageRepForCachingDisplay(in: view.bounds),
              let space = CGColorSpace(name: CGColorSpace.sRGB) else { return -1 }
        view.cacheDisplay(in: view.bounds, to: rep)
        guard let image = rep.cgImage,
              let context = CGContext(data: nil, width: image.width, height: image.height, bitsPerComponent: 8, bytesPerRow: image.width * 4,
                                      space: space, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue),
              let data = context.data else { return -1 }
        context.draw(image, in: CGRect(x: 0, y: 0, width: image.width, height: image.height))
        let scale = image.width / Int(view.bounds.width)
        let row = image.height - 1 - 10 * scale
        return Int(data.load(fromByteOffset: (row * image.width + x * scale) * 4, as: UInt8.self))
    }
}
