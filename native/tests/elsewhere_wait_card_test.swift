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
            ("dialog", "claude", "Claude has a dialog open in its terminal"),
        ]
        for test in cases {
            let heading = ElsewhereWaitCard.heading(kind: test.kind, surface: test.surface)
            guard heading == test.heading else {
                fatalError("\(test.kind) on \(test.surface): \(heading)")
            }
        }
        let shown: [(waiting: WaitingState?, readOnly: Bool, respond: Bool, shows: Bool)] = [
            (WaitingState(kind: "dialog", id: "d"), false, true, true),
            (WaitingState(kind: "approval", id: "a", answerable: true), false, true, false),
            (WaitingState(kind: "approval", id: "a"), false, true, true),
            (WaitingState(kind: "question", id: "q"), false, false, true),
            (WaitingState(kind: "approval", id: "a"), true, true, true),
            (nil, false, false, false),
        ]
        let hosted = WaitingPlace(WaitingState(kind: "approval", id: "a"), runtimeKind: "claude-host", lifecycle: SessionLifecycle(stop: true, resume: true))
        guard hosted == .restart, ElsewhereWaitCard.heading(kind: "approval", surface: "claude", place: hosted) == "Claude is waiting on a prompt Agenthail can't reach",
              ElsewhereWaitCard.detail(place: hosted, hostName: nil).contains("Stop and resume the session to clear it") else {
            fatalError("a hosted prompt no one holds: \(hosted)")
        }
        let terminal = WaitingPlace(WaitingState(kind: "approval", id: "a"), runtimeKind: "claude-cli", lifecycle: SessionLifecycle())
        guard terminal == .terminal, ElsewhereWaitCard.detail(place: terminal, hostName: nil).contains("in the session's terminal") else {
            fatalError("a terminal prompt: \(terminal)")
        }
        guard WaitingPlace(WaitingState(kind: "input", id: "i"), runtimeKind: "codex-app-server", lifecycle: SessionLifecycle()) == .elsewhere,
              WaitingPlace(WaitingState(kind: "approval", id: "a", answerable: true), runtimeKind: "claude-host", lifecycle: SessionLifecycle(stop: true, resume: true)) == .agenthail,
              WaitingPlace(WaitingState(kind: "dialog", id: "d"), runtimeKind: "claude-host", lifecycle: SessionLifecycle(stop: true, resume: true)) == .dialog else {
            fatalError("waiting places")
        }
        for test in shown where ElsewhereWaitCard.shows(test.waiting, readOnly: test.readOnly, respond: test.respond) != test.shows {
            fatalError("\(test.waiting?.kind ?? "none") readOnly=\(test.readOnly) respond=\(test.respond): expected shows=\(test.shows)")
        }
        _ = NSApplication.shared
        func height(at width: CGFloat) -> CGFloat {
            let card = ElsewhereWaitCard(waiting: WaitingState(kind: "question", id: "q"), place: .elsewhere, surface: "claude", hostName: "cmux", accent: AnyShapeStyle(Color.accentColor), open: {})
            return NSHostingView(rootView: card.frame(width: width)).fittingSize.height
        }
        let narrow = height(at: 300), wide = height(at: 900)
        guard narrow > wide + 10 else {
            fatalError("the card's explanation does not wrap at a narrow width: \(narrow) pt at 300 pt, \(wide) pt at 900 pt")
        }
        print("elsewhere wait card headings and wrapping ok")
    }
}
