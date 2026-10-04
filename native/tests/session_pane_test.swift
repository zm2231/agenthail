import Foundation

@main
struct SessionPaneTest {
    @MainActor
    static func main() async {
        let model = AgenthailModel(connecting: false)
        let first = model.openPane()
        let second = model.openPane()
        first.select("A")
        second.select("A")

        first.composer = "shared draft"
        check(second.composer == "shared draft", "panes on one session share its draft")

        second.select("B")
        check(second.composer.isEmpty, "another session starts with its own draft")
        second.composer = "only B"
        second.select("A")
        check(first.composer == "shared draft" && second.composer == "shared draft", "returning to a session keeps its draft")
        check(model.draft(for: "B").text == "only B", "leaving a session keeps its draft")

        model.draft(for: "A").restore("restored")
        check(first.composer == "shared draft\n\nrestored" && second.composer == first.composer, "restored text reaches every pane on the session")

        model.closePane(second)
        second.select("C")
        check(second.selectedSessionID == "A", "a closed pane ignores selection")
        check(first.selectedSessionID == "A", "closing one pane leaves the others")

        model.setTurnSettings(TurnSettings(effort: "high", mode: .plan), for: "A")
        check(model.turnSettings(for: "A") == TurnSettings(effort: "high", mode: .plan) && model.turnSettings(for: "B").isEmpty, "next-turn settings belong to one session")
        model.setTurnSettings(TurnSettings(), for: "A")
        check(model.turnSettings(for: "A").isEmpty, "resetting removes a session's next-turn settings")

        let working = SessionState(id: "W", surface: "codex", name: "w", alias: nil, status: "busy", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(), readOnly: nil, readOnlyReason: nil)
        check(SessionPane.stopAvailable(working, removed: false, draft: "  \n"), "an empty draft lets Stop interrupt a working session")
        check(!SessionPane.stopAvailable(working, removed: false, draft: "follow up"), "a draft turns Stop into queue or steer")
        check(!SessionPane.stopAvailable(working, removed: true, draft: ""), "a removed session cannot be stopped")
        let readOnly = SessionState(id: "R", surface: "codex", name: "r", alias: nil, status: "busy", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(), readOnly: true, readOnlyReason: nil)
        check(!SessionPane.stopAvailable(readOnly, removed: false, draft: ""), "a read-only session cannot be stopped")
        let idle = SessionState(id: "I", surface: "codex", name: "i", alias: nil, status: "idle", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(), readOnly: nil, readOnlyReason: nil)
        check(!SessionPane.stopAvailable(idle, removed: false, draft: "") && !SessionPane.stopAvailable(nil, removed: false, draft: ""), "nothing to stop when idle or unselected")

        let watched = model.openPane()
        watched.select("E")
        var changes = 0
        let observation = watched.objectWillChange.sink { changes += 1 }
        watched.composer = "a"
        check(!watched.draftIsEmpty && changes == 1, "typing the first character republishes the pane once")
        watched.composer = "ab"
        watched.composer = "abc"
        check(changes == 1, "further typing does not republish the pane")
        watched.composer = "  "
        check(watched.draftIsEmpty && changes == 2, "clearing the draft republishes the pane")
        model.draft(for: "E").text = "from another pane"
        check(!watched.draftIsEmpty, "edits from a pane sharing the draft are observed")
        watched.select("F")
        check(watched.draftIsEmpty, "a new session's empty draft is observed")
        observation.cancel()

        let delivered = await model.reply("  answer from a notification  ", to: "D", connectionTimeout: .milliseconds(50))
        check(!delivered && model.draft(for: "D").text == "answer from a notification", "an undeliverable reply waits in the session's draft")
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
