import AppKit
import SwiftUI
@testable import AgenthailHelpers

private struct Row: View {
    let increased: Bool
    var body: some View {
        Color.clear
            .frame(width: 120, height: 32)
            .modifier(Mark(increased: increased))
            .padding(4)
            .background(Color(.sRGB, red: 0x17 / 255, green: 0x18 / 255, blue: 0x1A / 255))
            .environment(\.textColors, TextColors(accentChoice: 0xE9A23B))
    }
}

private struct Mark: ViewModifier {
    let increased: Bool
    func body(content: Content) -> some View {
        content.modifier(SelectionMarkStyle(selected: true, shape: RoundedRectangle(cornerRadius: DesktopRadius.control), bar: true, increased: increased))
    }
}

@main
struct SelectionMarkTest {
    @MainActor
    static func main() {
        _ = NSApplication.shared
        let pane = (0x17, 0x18, 0x1A)
        let normal = render(increased: false)
        let inside = normal(64, 20)
        precondition(inside.0 > pane.0 + 20, "a selected row is washed with the accent, got \(inside)")

        let increased = render(increased: true)
        let plain = increased(64, 20)
        precondition(abs(plain.0 - pane.0) <= 2 && abs(plain.1 - pane.1) <= 2 && abs(plain.2 - pane.2) <= 2,
                     "under Increase Contrast a selected row leaves its text on the pane, got \(plain)")
        let edge = increased(123, 20)
        precondition(edge.0 > 0xC0 && edge.2 < 0x80, "and outlines it in the accent, got \(edge)")
        print("ok")
    }

    @MainActor
    private static func render(increased: Bool) -> (Int, Int) -> (Int, Int, Int) {
        let host = NSHostingView(rootView: Row(increased: increased))
        host.frame = NSRect(x: 0, y: 0, width: 128, height: 40)
        let window = NSWindow(contentRect: host.frame, styleMask: .borderless, backing: .buffered, defer: false)
        window.appearance = NSAppearance(named: .darkAqua)
        window.colorSpace = .sRGB
        window.contentView = host
        RunLoop.main.run(until: Date().addingTimeInterval(0.3))
        host.layoutSubtreeIfNeeded()
        let rep = host.bitmapImageRepForCachingDisplay(in: host.bounds)!
        host.cacheDisplay(in: host.bounds, to: rep)
        let image = rep.cgImage!
        let context = CGContext(data: nil, width: image.width, height: image.height, bitsPerComponent: 8, bytesPerRow: image.width * 4,
                                space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
        context.draw(image, in: CGRect(x: 0, y: 0, width: image.width, height: image.height))
        let bytes = Array(UnsafeBufferPointer(start: context.data!.assumingMemoryBound(to: UInt8.self), count: image.width * image.height * 4))
        let width = image.width, height = image.height
        let scale = width / Int(host.bounds.width)
        return { x, y in
            let offset = ((height - 1 - y * scale) * width + x * scale) * 4
            return (Int(bytes[offset]), Int(bytes[offset + 1]), Int(bytes[offset + 2]))
        }
    }
}
