@testable import AgenthailHelpers
import AppKit
import SwiftUI

@MainActor
final class Seen {
    var ids: Set<UUID> = []
    func record(_ id: UUID) { ids.insert(id) }
}

struct Probe: View {
    let seen: Seen
    @State private var id = UUID()

    var body: some View {
        let _ = seen.record(id)
        Text("probe")
    }
}

struct Branching: ViewModifier {
    @Environment(\.windowBackdrop) private var backdrop

    func body(content: Content) -> some View {
        if let backdrop { BranchingBody(content: content, backdrop: backdrop) } else { content }
    }
}

struct BranchingBody<Content: View>: View {
    let content: Content
    @ObservedObject var backdrop: WindowBackdrop

    var body: some View {
        if backdrop.active && !backdrop.solid { content.background(.gray) } else { content }
    }
}

@main
struct GlassIdentityTest {
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

        var art = UIConfig()
        art.values["background"] = .text("image: art/a-dark.jpg")
        art.values["background.light"] = .text("image: art/a-light.jpg")
        art.values["glass.gutter"] = .number(8)
        let wallpapers = ["art/a-dark.jpg": WallpaperImage(id: "art/a-dark.jpg", image: image()), "art/a-light.jpg": WallpaperImage(id: "art/a-light.jpg", image: image())]

        let modifiers: [(String, Bool, (Probe) -> AnyView)] = [
            ("floatingGlass", true, { AnyView($0.floatingGlass(.gray, in: RoundedRectangle(cornerRadius: 8))) }),
            ("glassPane(.content)", true, { AnyView($0.glassPane(.content, fallback: .gray)) }),
            ("glassPane(.sidebar)", true, { AnyView($0.glassPane(.sidebar, fallback: .gray, gaps: [.leading])) }),
            ("glassPane(.chrome)", true, { AnyView($0.glassPane(.chrome, fallback: .gray, edge: .top)) }),
            ("solidContentPane", true, { AnyView($0.solidContentPane(gaps: .all)) }),
            ("branching control", false, { AnyView($0.modifier(Branching())) }),
        ]
        for (name, keeps, wrap) in modifiers {
            let seen = Seen()
            let backdrop = WindowBackdrop()
            let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 240, height: 160), styleMask: [.titled], backing: .buffered, defer: false)
            window.appearance = NSAppearance(named: .darkAqua)
            let hosting = NSHostingView(rootView: wrap(Probe(seen: seen)).environment(\.windowBackdrop, backdrop))
            window.contentView = hosting
            backdrop.windowChanged(window)
            settle(hosting)
            var states: Set<String> = []
            func flip(_ change: () -> Void) {
                change()
                backdrop.windowChanged(window)
                settle(hosting)
                states.insert(!backdrop.active ? "plain" : !backdrop.solid ? "glass" : backdrop.gutter > 0 ? "solid card" : "solid")
            }
            flip { backdrop.configure(art, wallpapers: wallpapers) }
            flip { window.appearance = NSAppearance(named: .aqua) }
            flip { window.appearance = NSAppearance(named: .darkAqua) }
            flip { backdrop.configure(UIConfig(), wallpapers: [:]) }
            flip { backdrop.configure(art, wallpapers: wallpapers) }
            check(states == ["glass", "solid card", "plain"], "\(name): the backdrop went through glass, solid with a gutter, and plain, got \(states)")
            if keeps {
                check(seen.ids.count == 1, "\(name): the content kept its state through every backdrop change, got \(seen.ids.count) identities")
            } else {
                check(seen.ids.count > 1, "\(name): a modifier that branches around its content loses it as the backdrop changes, got \(seen.ids.count) identities")
            }
        }
        if failures > 0 { exit(1) }
        print("glass identity tests passed")
    }

    @MainActor
    static func settle(_ hosting: NSView) {
        for _ in 0..<5 {
            RunLoop.main.run(until: Date().addingTimeInterval(0.02))
            hosting.layoutSubtreeIfNeeded()
        }
    }

    static func image() -> CGImage {
        let context = CGContext(data: nil, width: 4, height: 4, bitsPerComponent: 8, bytesPerRow: 16, space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
        context.setFillColor(CGColor(red: 0.2, green: 0.3, blue: 0.5, alpha: 1))
        context.fill(CGRect(x: 0, y: 0, width: 4, height: 4))
        return context.makeImage()!
    }
}
