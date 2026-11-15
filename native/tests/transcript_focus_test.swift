@testable import AgenthailHelpers
import Foundation

@main
struct TranscriptFocusTest {
    static func item(_ id: String, _ kind: String, _ title: String = "", role: String? = nil, call: String? = nil, agent: AgentMessage? = nil) -> TimelineItem {
        var value = TimelineItem(id: id, kind: kind, role: role, title: title, text: id, timestamp: nil, callId: call, status: nil, truncated: false, truncationReason: nil, bodyRef: nil)
        value.agent = agent
        return value
    }

    static func main() {
        let peer = AgentMessage(direction: "in", kind: "peer", peer: "builder", summary: "Ready")
        let send = AgentMessage(direction: "out", kind: "send", peer: "builder", summary: "Go")
        let items = [
            item("ask-person", "message", role: "user"),
            item("reasoning", "reasoning"),
            item("bash", "toolCall", "Bash", call: "b"),
            item("bash-out", "toolResult", "Bash", call: "b"),
            item("question", "toolCall", "AskUserQuestion", call: "q"),
            item("answer", "toolResult", "AskUserQuestion", call: "q"),
            item("mcp-ask", "toolCall", "mcp__agenthail__agenthail_ask", call: "m"),
            item("codex-ask", "toolCall", "request_user_input", call: "c"),
            item("compacted", "event", "Context compacted"),
            item("notice", "notice", "Notice"),
            item("peer", "message", "builder", role: "peer", agent: peer),
            item("send", "toolCall", "agenthail_send", call: "s", agent: send),
            item("sent", "toolResult", "agenthail_send", call: "s"),
            item("streamed", "text", role: "assistant"),
            item("file", "file", role: "assistant"),
            item("image", "attachment", role: "assistant"),
            item("reply", "message", role: "assistant"),
            item("duration", "event", "Turn duration"),
            item("done", "done"),
            item("goal", "goal"),
        ]
        let kept = TranscriptFocus.kept(items).map(\.id)
        let expected = ["ask-person", "question", "answer", "mcp-ask", "codex-ask", "peer", "send", "sent", "streamed", "file", "image", "reply", "duration"]
        check(kept == expected, "focus keeps messages, files, asks and agent messages in order: \(kept)")

        let orphan = TranscriptFocus.kept([item("late", "toolResult", call: "q"), item("question", "toolCall", "AskUserQuestion", call: "q")]).map(\.id)
        check(orphan == ["question"], "a result before its ask is not kept: \(orphan)")

        let blocks = TranscriptBlock.build(TranscriptFocus.kept(items), foldAgents: false)
        let tools = blocks.filter { if case .tools = $0.kind { true } else { false } }
        check(tools.count == 1, "the asks form the only tool block: \(blocks.map(\.kind))")
        if let last = blocks.last { check(last.finished == "duration", "the reply keeps its turn time: \(last)") }
        check(!blocks.contains { if case .notice = $0.kind { true } else { false } }, "notices are hidden")
        print("ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
