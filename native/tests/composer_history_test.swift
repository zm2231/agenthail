@testable import AgenthailHelpers
import Foundation

@main
struct ComposerHistoryTest {
    @MainActor
    static func main() {
        let draft = ComposerDraft()
        check(!draft.toggleStash(), "stashing an empty composer with nothing stashed does nothing")
        draft.text = "half written"
        check(draft.toggleStash() && draft.text.isEmpty && draft.stash?.text == "half written", "⌃S stashes the draft and clears the composer")
        draft.text = "a quick question"
        check(draft.toggleStash() && draft.text == "half written" && draft.stash?.text == "a quick question", "stashing while a stash is held swaps the two")
        draft.toggleStash()
        draft.text = ""
        check(draft.toggleStash() && draft.text == "half written" && draft.stash == nil, "⌃S on an empty composer brings the stash back")

        draft.text = "kept aside"
        draft.toggleStash()
        draft.text = "sent first"
        draft.recordSending(draft.text)
        draft.text = ""
        draft.restoreStashAfterSend()
        check(draft.text == "kept aside" && draft.stash == nil, "sending brings the stash back into the composer")
        draft.text = "typing again"
        draft.toggleStash()
        draft.text = "not sent yet"
        draft.restoreStashAfterSend()
        check(draft.text == "not sent yet" && draft.stash?.text == "typing again", "the stash never overwrites a draft in progress")
        draft.text = ""
        draft.toggleStash()

        let history = ComposerDraft()
        history.recordSent([prompt("newest", "s"), prompt("older", "s"), prompt("newest", "s"), prompt("oldest", "s")])
        history.text = "in progress"
        var shown: [String] = []
        while let text = history.history.older(from: history.text) {
            history.text = text
            shown.append(text)
        }
        check(shown == ["newest", "older", "oldest"], "↑ steps back through what was sent, each prompt once: \(shown)")
        check(history.history.isBrowsing, "stopping at the oldest keeps browsing, so ↑ there does not leave the list")
        var back: [String] = []
        while history.history.isBrowsing, let text = history.history.newer() { back.append(text) }
        check(back == ["older", "newest", "in progress"], "↓ steps forward and ends at the draft in progress: \(back)")
        check(history.history.newer() == nil, "↓ after the draft is the text view's own")

        history.recordSending("just sent")
        history.recordSent([prompt("newest", "s")])
        check(history.history.older(from: "") == "just sent", "a prompt sent from here stays first until the host records it")
        history.history.stopBrowsing()
        history.recordSent([prompt("just sent", "s"), prompt("newest", "s")])
        check(history.sent.map(\.text) == ["just sent", "newest"], "once recorded, the local copy is dropped: \(history.sent.map(\.text))")

        history.text = "never sent"
        check(history.clearIntoHistory() && history.text.isEmpty, "Esc Esc clears the draft")
        check(history.history.older(from: "") == "never sent", "the cleared draft is the first thing ↑ brings back")
        history.history.stopBrowsing()
        history.text = "  \n"
        check(!history.clearIntoHistory(), "Esc Esc on an empty draft clears nothing, so it can list the prompts instead")

        let page = try! JSONDecoder().decode(PromptsPage.self, from: Data(#"{"prompts":[{"sessionId":"s","itemId":"i","turnId":"t","ts":"2026-10-20T10:00:00.25Z","text":"hi"},{"sessionId":"s","itemId":"j","ts":"2026-10-20T10:00:00Z","text":"yo"}],"nextBefore":""}"#.utf8))
        check(page.prompts.count == 2 && page.prompts[1].turnId == nil, "prompts decode with and without a turn")
        check(page.prompts[0].sentAt.map { $0 > page.prompts[1].sentAt! } == true, "fractional and whole-second times both parse and order")
        print("composer history: ok")
    }

    private static func prompt(_ text: String, _ session: String) -> PromptItem {
        PromptItem(sessionId: session, itemId: UUID().uuidString, turnId: nil, ts: "", text: text)
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
