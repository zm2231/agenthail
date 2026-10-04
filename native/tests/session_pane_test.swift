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
