@testable import AgenthailHelpers
import AppKit
import SwiftUI

@main
struct ConversationHeaderTest {
    @MainActor
    static func main() {
        _ = NSApplication.shared
        var capabilities = Capabilities()
        capabilities.stream = true
        var session = SessionState(id: "s", surface: "claude", name: "Fix the flaky build in the release pipeline", alias: nil, status: "idle", lastActive: "2026-11-06T08:00:00Z", queueCount: 0, open: false, current: true, currentReason: nil, capabilities: capabilities, readOnly: false, readOnlyReason: "")
        session.runtime = SessionRuntime(launcher: "tmux", focusable: true)
        for width in [300.0, 420.0, 900.0] {
            let header = ConversationHeader(session: session, title: session.name, inspectorVisible: .constant(false), onCall: {})
            let fitted = NSHostingController(rootView: header).sizeThatFits(in: CGSize(width: width, height: ConversationHeader.height))
            check(fitted.width <= width + 0.5, "at \(Int(width)) pt the header fits instead of clipping, laid out at \(fitted.width)")
        }
        let header = ConversationHeader(session: session, title: session.name, inspectorVisible: .constant(false), onCall: {})
        let whole = NSHostingView(rootView: header).fittingSize
        check(whole.width <= 900, "at 900 pt the title, state label, live facts and Open in all fit whole, needing \(whole.width)")
        print("conversation header tests passed")
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
