@testable import AgenthailHelpers
import Foundation

@main
struct AgentRowCompactTest {
    static func notice(_ id: String, _ text: String) -> TimelineItem {
        var value = TimelineItem(id: id, kind: "event", role: "system", title: "Usage limit reset", text: text, timestamp: nil, callId: nil, status: nil, truncated: false, truncationReason: nil, bodyRef: nil)
        value.agent = AgentMessage(direction: "in", kind: "notice", peer: "Usage limit reset", summary: text)
        return value
    }

    static func main() {
        check(AgentRowStyle.previewLines == 1, "an agent message previews on one line")
        check(!AgentRowStyle.expandable(text: "Back online.", truncated: false, files: 0), "a short one-line message has nothing to expand")
        check(AgentRowStyle.expandable(text: "Done.\nDetails follow.", truncated: false, files: 0), "a message with more lines expands")
        check(AgentRowStyle.expandable(text: "x", truncated: true, files: 0) && AgentRowStyle.expandable(text: "x", truncated: false, files: 1), "a cut line or a file expands")

        let resume = "Your usage limit has reset. Continue the work that was interrupted."
        let reply = TimelineItem(id: "r", kind: "message", role: "assistant", title: "assistant", text: "Continuing", timestamp: nil, callId: nil, status: nil, truncated: false, truncationReason: nil, bodyRef: nil)
        let six = (1...6).map { notice("n\($0)", resume) }
        let blocks = TranscriptBlock.build(six + [reply])
        check(blocks.count == 2, "six identical notices are one row: \(blocks.map(\.kind))")
        if case .agent(let entry) = blocks.first?.kind {
            check(entry.repeats == 6 && entry.chip == "Usage limit reset ×6", "the row counts them: \(entry.chip)")
            check(entry.item.id == "n6" && blocks[0].id == TranscriptRowID.item("n1"), "the row shows the newest and keeps the first id")
        }
        let mixed = TranscriptBlock.build([notice("a", resume), notice("b", "Session moved"), notice("c", resume), reply])
        guard case .agentFold(let fold) = mixed.first?.kind else {
            check(false, "different notices stay separate and fold: \(mixed.map(\.kind))")
            return
        }
        check(fold.members.count == 3, "only identical neighbours merge: \(fold.members.count)")
        check(AgentRepeat.runs([]).isEmpty && AgentRepeat.runs(["a", nil, nil, "a", "a"]) == [3..<5], "runs need two equal keys in a row")

        var queued = LocalSend(text: "later", sentAt: Date(), status: "Queued")
        queued.queueID = 42
        let sent = LocalSend(text: "now", sentAt: Date(), status: "Sent")
        check(LocalSend.shown([queued, sent]).map(\.text) == ["now"], "a queued send shows in the queue tray, not the transcript")
        let receipt = { (json: String) in try? JSONDecoder().decode(DeliveryReceipt.self, from: Data(json.utf8)) }
        check(LocalSend.queueID(of: receipt(#"{"status":"queued","queueId":7}"#)) == 7, "a queued receipt names its queue row")
        check(LocalSend.queueID(of: receipt(#"{"status":"queued","queueId":7,"movedTo":"other"}"#)) == nil, "a send queued for another session stays in this transcript")
        check(LocalSend.queueID(of: receipt(#"{"status":"sent","queueId":7}"#)) == nil, "a delivered send is not held")
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
