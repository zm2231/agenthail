@testable import AgenthailHelpers
import AppKit
import SwiftUI

@main
struct ElsewhereWaitCardTest {
    @MainActor
    static func main() {
        let cases: [(kind: String, surface: String, heading: String)] = [
            ("question", "claude", "Claude asks you a question"),
            ("input", "codex", "Codex needs your input"),
            ("approval", "codex", "Codex needs your approval"),
        ]
        for test in cases {
            let heading = ElsewhereWaitCard.heading(kind: test.kind, surface: test.surface)
            guard heading == test.heading else {
                fatalError("\(test.kind) on \(test.surface): \(heading)")
            }
        }
        _ = NSApplication.shared
        func height(at width: CGFloat) -> CGFloat {
            let card = ElsewhereWaitCard(waiting: WaitingState(kind: "question", id: "q"), surface: "claude", hostName: "cmux", accent: .accentColor, open: {})
            return NSHostingView(rootView: card.frame(width: width)).fittingSize.height
        }
        let narrow = height(at: 300), wide = height(at: 900)
        guard narrow > wide + 10 else {
            fatalError("the card's explanation does not wrap at a narrow width: \(narrow) pt at 300 pt, \(wide) pt at 900 pt")
        }
        print("elsewhere wait card headings and wrapping ok")
    }
}
