import SwiftUI
import XCTest
@testable import Agenthail

@MainActor
private final class Seen {
    var ids: Set<UUID> = []
    func record(_ id: UUID) { ids.insert(id) }
}

private struct Probe: View {
    let seen: Seen
    @State private var id = UUID()

    var body: some View {
        let _ = seen.record(id)
        Text("probe")
    }
}

private struct Branching: ViewModifier {
    @Environment(\.phoneAppearance) private var appearance

    func body(content: Content) -> some View {
        if let appearance { BranchingBody(content: content, appearance: appearance) } else { content }
    }
}

private struct BranchingBody<Content: View>: View {
    let content: Content
    @ObservedObject var appearance: PhoneAppearance
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        if appearance.active && !appearance.config.solidSurfaces(dark: colorScheme == .dark, reduceTransparency: false) {
            content.background(.gray)
        } else {
            content
        }
    }
}

final class GlassIdentityTests: XCTestCase {
    private func config(_ values: [String: Any]) throws -> UIConfig {
        let data = try JSONSerialization.data(withJSONObject: ["rev": "r1", "values": values, "colors": [String: Any](), "chosen": [String: String](), "overrides": [String: String](), "problems": []])
        return try JSONDecoder().decode(UIConfig.self, from: data)
    }

    @MainActor
    func testEveryAppearanceModifierKeepsItsContentThroughAppearanceChanges() throws {
        let mesh = "mesh: 2x2, #102030, #203040, #304050, #405060"
        let glass = try config(["background": mesh, "glass": "on"])
        let solid = try config(["background": mesh, "glass": "off"])
        let plain = try config([:])
        let modifiers: [(String, Bool, (Probe) -> AnyView)] = [
            ("floatingGlass", true, { AnyView($0.floatingGlass(in: Capsule())) }),
            ("phoneBackdrop(.transcript)", true, { AnyView($0.phoneBackdrop(.transcript)) }),
            ("phoneBackdrop(.list)", true, { AnyView($0.phoneBackdrop(.list)) }),
            ("solidTopBarGround", true, { AnyView($0.solidTopBarGround()) }),
            ("solidTabBar", true, { AnyView($0.solidTabBar()) }),
            ("branching control", false, { AnyView($0.modifier(Branching())) }),
        ]
        for (name, keeps, wrap) in modifiers {
            let seen = Seen()
            let appearance = PhoneAppearance(cache: nil)
            let host = UIHostingController(rootView: AppearanceScope(appearance: appearance) { wrap(Probe(seen: seen)) })
            let window = UIWindow(frame: CGRect(x: 0, y: 0, width: 390, height: 600))
            window.rootViewController = host
            window.overrideUserInterfaceStyle = .dark
            window.isHidden = false
            settle(host)
            var states: Set<String> = []
            func flip(_ change: () -> Void) {
                change()
                settle(host)
                let dark = window.overrideUserInterfaceStyle == .dark
                states.insert(appearance.active ? (appearance.config.solidSurfaces(dark: dark, reduceTransparency: false) ? "solid" : "glass") : "plain")
            }
            flip { appearance.preview(glass) }
            flip { window.overrideUserInterfaceStyle = .light }
            flip { window.overrideUserInterfaceStyle = .dark }
            flip { appearance.preview(solid) }
            flip { appearance.preview(plain) }
            flip { appearance.preview(glass) }
            XCTAssertEqual(states, ["glass", "solid", "plain"], "\(name): the appearance went through glass, solid and plain")
            if keeps {
                XCTAssertEqual(seen.ids.count, 1, "\(name): the content kept its state through every appearance change")
            } else {
                XCTAssertGreaterThan(seen.ids.count, 1, "\(name): a modifier that branches around its content loses it as the appearance changes")
            }
            window.isHidden = true
        }
    }

    @MainActor
    private func settle(_ host: UIViewController) {
        for _ in 0..<5 {
            RunLoop.main.run(until: Date().addingTimeInterval(0.02))
            host.view.layoutIfNeeded()
        }
    }
}
