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
        var peer = notice("p", "Branch is ready")
        peer.agent = AgentMessage(direction: "in", kind: "peer", peer: "@builder", summary: "Branch is ready")
        let counted = TranscriptBlock.build([notice("x", resume), notice("y", resume), peer, reply])
        if case .agentFold(let fold) = counted.first?.kind, case .agent(let first) = fold.members.first?.kind {
            check(fold.label == "3 updates · 2 Agenthail notices, 1 agent message", "the fold counts every notice it holds: \(fold.label)")
            check(first.chip == "Usage limit reset ×2", "an opened fold keeps the count: \(first.chip)")
        } else {
            check(false, "repeated notices and another message fold together: \(counted.map(\.kind))")
        }
        check(AgentRepeat.runs([]).isEmpty && AgentRepeat.runs(["a", nil, nil, "a", "a"]) == [3..<5], "runs need two equal keys in a row")

        var queued = LocalSend(text: "later", sentAt: Date(), status: "Queued")
        queued.queueID = 42
        let sent = LocalSend(text: "now", sentAt: Date(), status: "Sent")
        func row(_ status: String, operation: String = "message") -> QueueState {
            try! JSONDecoder().decode(QueueState.self, from: Data(#"{"id":42,"sessionId":"S","target":"S","message":"later","status":"\#(status)","operation":"\#(operation)","attempts":0,"queuedAt":"2026-01-01T00:00:00Z","evidence":"queued"}"#.utf8))
        }
        check(LocalSend.shown([queued, sent], queue: [row("pending")]).map(\.text) == ["now"], "a send waiting in the queue tray shows there, not in the transcript")
        for status in ["dead", "inflight", "delivered", "canceled"] {
            check(LocalSend.shown([queued, sent], queue: [row(status)]).map(\.text) == ["later", "now"], "a send the tray does not show stays in the transcript: \(status)")
        }
        check(LocalSend.shown([queued, sent], queue: []).map(\.text) == ["later", "now"], "a send the queue does not list stays in the transcript")

        func session(send: Bool, readOnly: Bool = false) -> SessionState {
            let json = #"{"id":"S","surface":"claude","name":"builder","status":"busy","queueCount":1,"open":true,"current":true,"readOnly":\#(readOnly),"capabilities":{"send":\#(send),"stream":true,"reply":true,"goal":false,"compact":false,"model":false,"interrupt":false,"steer":false,"respond":false}}"#
            return try! JSONDecoder().decode(SessionState.self, from: Data(json.utf8))
        }
        check(QueueFacts.tray([row("pending")], for: session(send: true)).map(\.id) == [42], "a session that takes messages shows its queue tray")
        check(QueueFacts.tray([row("pending")], for: session(send: false)).isEmpty && QueueFacts.tray([row("pending")], for: session(send: true, readOnly: true)).isEmpty, "a session that can't take messages shows no tray")
        check(LocalSend.shown([queued], queue: QueueFacts.tray([row("pending")], for: session(send: false))).count == 1, "with no tray the queued send stays in the transcript")

        MainActor.assumeIsolated {
            let outbox = Outbox()
            outbox.record(queued, in: "S")
            outbox.record(sent, in: "S")
            outbox.dropQueued(7, in: "S")
            check(outbox.rows["S"]?.count == 2, "dropping another queue row leaves the send")
            outbox.dropQueued(42, in: "S")
            check(outbox.rows["S"]?.map(\.text) == ["now"], "a queued send the person removed, took back or steered leaves the transcript")
        }
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
