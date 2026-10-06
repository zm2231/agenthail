import AppKit
import SwiftUI

@MainActor
final class Measured {
    var banner: CGFloat = 0
}

struct Column: View {
    let session: SessionState
    let measured: Measured

    var body: some View {
        VStack(spacing: 0) {
            Color.clear.frame(height: 52)
            SharedConversationBanner(note: SharedConversation.note(session) ?? "", peers: SharedConversation.peers(session), onOpen: { _ in })
                .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { measured.banner = $0 }
            ScrollView { Text("Fixture transcript") }
                .safeAreaInset(edge: .bottom, spacing: 0) { Color.clear.frame(height: 150) }
        }
    }
}

@main
struct SharedConversationBannerTest {
    @MainActor
    static func main() {
        _ = NSApplication.shared
        for peers in [1, 2, 40] {
            let session = fixture(peers: peers)
            check(SharedConversation.note(session) != nil, "\(peers) peers: the fixture is a shared conversation")
            let result = layout(session)
            check(result.frame == 600, "\(peers) peers: the banner never makes the column taller than the window, got \(result.frame)")
            check(result.minimum < 600, "\(peers) peers: the banner never raises the window minimum height to the window height, got \(result.minimum)")
            check(result.banner > 0 && result.banner <= 9 + 9 + 20 + 5 + 120, "\(peers) peers: the banner stays within its note and capped peer list, got \(result.banner)")
        }
    }

    private static func fixture(peers: Int) -> SessionState {
        let shared = (0..<peers).map { SharedProcess(id: "fixture-peer-\($0)", pid: 4100 + $0, status: "idle", startedAt: "2026-01-02T15:04:00Z") }
        return SessionState(id: "fixture-session", surface: "claude", name: "Fixture session", alias: nil, status: "idle", lastActive: nil, queueCount: 0, open: true, current: false, capabilities: Capabilities(), readOnly: nil, readOnlyReason: nil, sharedWith: shared)
    }

    @MainActor
    private static func layout(_ session: SessionState) -> (frame: CGFloat, minimum: CGFloat, banner: CGFloat) {
        let measured = Measured()
        let hosting = NSHostingView(rootView: Column(session: session, measured: measured))
        hosting.sizingOptions = [.minSize]
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1000, height: 600), styleMask: [.titled, .resizable], backing: .buffered, defer: false)
        window.contentView = hosting
        hosting.layoutSubtreeIfNeeded()
        RunLoop.main.run(until: Date().addingTimeInterval(0.05))
        return (hosting.frame.height, window.contentMinSize.height, measured.banner)
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
