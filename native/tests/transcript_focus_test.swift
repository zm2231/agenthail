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
        let focused = TranscriptFocus.focused(items).map(\.id)
        let expected = ["ask-person", "question", "answer", "mcp-ask", "codex-ask", "reasoning", "bash", "bash-out", "peer", "send", "sent", "streamed", "file", "image", "reply", "duration", "done", "goal"]
        check(focused == expected, "focus keeps what was said and asked in place, and a turn without a reply keeps its work at its end: \(focused)")

        let blocks = TranscriptBlock.build(TranscriptFocus.focused(items), foldAgents: false, focus: true)
        let tools = blocks.compactMap { if case .tools(let run) = $0.kind { run.map(\.id) } else { nil } }
        check(tools == [["question", "answer", "mcp-ask", "codex-ask"], ["reasoning", "bash", "bash-out"]], "an ask stays a run of its own beside the work, which ends the turn before the helper's message: \(tools)")
        let reply = blocks.first { $0.id == TranscriptRowID.item("reply") }
        check(reply?.finished == "duration", "the reply keeps its turn time: \(String(describing: reply))")
        check(!blocks.contains { if case .notice = $0.kind { true } else { false } }, "notices are hidden")
        let work = items.filter { ["reasoning", "bash", "bash-out"].contains($0.id) }
        check(ToolRunSummary.folds(ToolRunSummary.entries(work), focus: true), "the work folds in focus even with one call")
        check(TranscriptFocus.isAsk(item("bare-ask", "toolCall", "agenthail_ask", call: "a")), "a bare agenthail_ask is an ask, as a bare end_turn is an end_turn")
        let askRun = items.filter { ["question", "answer"].contains($0.id) }
        check(!ToolRunSummary.folds(ToolRunSummary.entries(askRun), focus: true), "an ask never folds")

        let turns = [
            item("p1", "message", role: "user"),
            item("say1", "message", role: "assistant"),
            item("t1", "toolCall", "Bash", call: "1"),
            item("say2", "message", role: "assistant"),
            item("t2", "toolCall", "Read", call: "2"),
            item("helper", "message", role: "peer", agent: peer),
            item("t3", "toolCall", "Bash", call: "3"),
            item("p2", "message", role: "user"),
            item("t4", "toolCall", "Bash", call: "4"),
        ]
        let order = TranscriptFocus.focused(turns).map(\.id)
        check(order == ["p1", "say1", "t1", "t2", "say2", "helper", "t3", "p2", "t4"], "a turn ends at the next prompt or helper message, and its work comes just before its last reply: \(order)")
        var timed = turns.filter { $0.kind == "toolCall" }
        timed[0].timestamp = "2026-11-17T10:00:00Z"
        timed[1].timestamp = "2026-11-17T10:06:00Z"
        check(ToolRunSummary.focusLine(Array(timed.prefix(2))) == "6m of work · 2 steps", "the work line: \(ToolRunSummary.focusLine(Array(timed.prefix(2))))")
        check(ToolRunSummary.focusLine([timed[2]]) == "1 step", "a single untimed step: \(ToolRunSummary.focusLine([timed[2]]))")

        let replied = [
            item("ask", "message", role: "user"),
            item("note", "message", role: "assistant"),
            item("run", "toolCall", "Bash", call: "r"),
            item("run-out", "toolResult", "Bash", call: "r"),
            item("answer", "message", role: "assistant"),
            item("took", "event", "Turn duration"),
        ]
        let read = TranscriptFocus.focused(replied).map(\.id)
        check(read == ["ask", "note", "run", "run-out", "answer", "took"], "the turn's work sits above its final reply, as in the full transcript, and the reply reads last: \(read)")
        let after = [
            item("ask", "message", role: "user"),
            item("answer", "message", role: "assistant"),
            item("late", "toolCall", "Bash", call: "l"),
            item("late-out", "toolResult", "Bash", call: "l"),
            item("took", "event", "Turn duration"),
        ]
        let moved = TranscriptFocus.focused(after)
        check(moved.map(\.id) == ["ask", "late", "late-out", "answer", "took"], "work after the reply moves above it: \(moved.map(\.id))")
        let timedReply = TranscriptBlock.build(moved, foldAgents: false, focus: true).first { $0.id == TranscriptRowID.item("answer") }
        check(timedReply?.finished == "took", "a reply the work moved above keeps its turn time: \(String(describing: timedReply))")

        let ended = [
            item("bash", "toolCall", "Bash", call: "b"),
            item("end", "toolCall", "mcp__agenthail__agenthail_end_turn", call: "e"),
            item("end-ok", "toolResult", "Tool result", call: "e"),
            item("codex-end", "toolCall", "agenthail_end_turn", call: "ce"),
            item("codex-end-ok", "toolResult", "Tool result", call: "ce"),
            item("bash-out", "toolResult", "Tool result", call: "b"),
            item("lookalike", "toolCall", "mcp__other__agenthail_end_turn_later", call: "l"),
            item("reply", "message", role: "assistant"),
        ]
        let shown = TranscriptFocus.shown(ended).map(\.id)
        check(shown == ["bash", "bash-out", "lookalike", "reply"], "end-of-turn calls and their results are hidden, nothing else: \(shown)")
        let inline = String(AgentMessage.inline("**Reuters.** Zain *opened* it\n- next").characters)
        check(inline == "Reuters. Zain opened it\n- next", "an agent message reads without Markdown marks and keeps its lines: \(inline.debugDescription)")
        print("ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
