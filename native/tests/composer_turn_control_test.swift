@testable import AgenthailHelpers
import Foundation

@main
struct ComposerTurnControlTest {
    static func main() {
        var seen: [String] = []
        var mode = "manual"
        for _ in 0..<4 {
            mode = SessionRequests.cycled(from: mode)
            seen.append(mode)
        }
        check(seen == ["acceptEdits", "plan", "auto", "manual"], "⇧Tab cycles manual, accept edits, plan and auto: \(seen)")
        check(SessionRequests.cycled(from: "") == "acceptEdits" && SessionRequests.cycled(from: "default") == "acceptEdits", "a session in its settings' default moves on to accept edits")
        check(SessionRequests.cycled(from: "bypassPermissions") == "manual" && SessionRequests.cycled(from: "dontAsk") == "manual", "leaving a menu-only mode goes back to manual")
        check(!SessionRequests.cyclingModes.contains("bypassPermissions") && !SessionRequests.cyclingModes.contains("dontAsk"), "cycling never lands on bypass permissions or don't ask")
        check(SessionRequests.cyclingModes.allSatisfy(SessionRequests.permissionModes.contains), "every cycled mode is one a session can be set to")

        check(TurnSettings.Mode.cycled(from: nil) == .plan && TurnSettings.Mode.cycled(from: .plan) == .default && TurnSettings.Mode.cycled(from: .default) == .plan, "Codex switches between Plan and Work")

        check(QueueTakeBack.text(queued: ["first", "second"], draft: "now") == "first\n\nsecond\n\nnow", "taken-back messages go ahead of the draft, oldest first")
        check(QueueTakeBack.text(queued: ["first"], draft: "  \n") == "first", "an empty draft adds nothing after them")
        check(QueueTakeBack.text(queued: [], draft: "now") == "now", "nothing taken back leaves the draft alone")
        print("composer turn control: ok")
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
