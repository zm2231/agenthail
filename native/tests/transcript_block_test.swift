@testable import AgenthailHelpers
import Foundation

@main
struct TranscriptBlockTest {
    static func item(_ kind: String, _ title: String, text: String = "", role: String? = nil, status: String? = nil) -> TimelineItem {
        TimelineItem(id: UUID().uuidString, kind: kind, role: role, title: title, text: text, timestamp: nil, callId: nil, status: status, truncated: false, truncationReason: nil, bodyRef: nil)
    }

    static func agentItem(_ kind: String, _ title: String, text: String, role: String, agent: AgentMessage, call: String? = nil, status: String? = nil) -> TimelineItem {
        var value = TimelineItem(id: UUID().uuidString, kind: kind, role: role, title: title, text: text, timestamp: nil, callId: call, status: status, truncated: false, truncationReason: nil, bodyRef: nil)
        value.agent = agent
        return value
    }

    static func injected() {
        let builder = AgentMessage(direction: "in", kind: "peer", peer: "builder", summary: "Branch is ready")
        let anonymous = AgentMessage(direction: "in", kind: "peer", peer: "", summary: "Hello")
        let task = AgentMessage(direction: "in", kind: "task", peer: "Task notification", summary: "Tests passed")
        let peer = agentItem("message", "builder", text: "Branch is ready", role: "peer", agent: builder)
        let other = agentItem("message", "Agent message", text: "Hello", role: "peer", agent: anonymous)
        let notice = agentItem("event", "Task notification", text: "Tests passed", role: "system", agent: task, status: "completed")
        let reply = item("message", "assistant", text: "On it", role: "assistant")
        let blocks = TranscriptBlock.build([peer, other, notice, reply])
        guard blocks.count == 2, case .agentFold(let fold) = blocks[0].kind else {
            check(false, "consecutive agent items fold before a reply: \(blocks.map(\.kind))")
            return
        }
        check(fold.label == "3 updates · 2 agent messages, 1 task notice" && fold.senders == "builder, another agent, Task notification", "the fold names its kinds: \(fold)")
        check(fold.members.map(\.id) == [peer.id, other.id, notice.id] && blocks[0].id == "agents:" + peer.id, "the fold keeps its members in order")
        if case .agent(let entry) = fold.members[2].kind {
            check(entry.chip == "Task notification · completed", "a task notice names its status: \(entry.chip)")
        }
        let trailing = TranscriptBlock.build([reply, peer, other]).map(\.kind)
        check(trailing.count == 3 && trailing.dropFirst().allSatisfy { if case .agent = $0 { true } else { false } }, "the trailing run stays unfolded: \(trailing)")
        let open = TranscriptBlock.build([peer, other, reply], foldAgents: false).map(\.kind)
        check(open.count == 3, "expand all leaves every agent message on its own: \(open)")
    }

    static func outgoingSend() {
        let send = AgentMessage(direction: "out", kind: "send", peer: "@collab", summary: "Tests pass", body: "Tests pass\nDetails")
        let call = agentItem("toolCall", "Bash", text: "{}", role: "assistant", agent: send, call: "k")
        let result = TimelineItem(id: "result", kind: "toolResult", role: nil, title: "Bash", text: "not delivered", timestamp: nil, callId: "k", status: "failed", truncated: false, truncationReason: nil, bodyRef: nil)
        let blocks = TranscriptBlock.build([item("toolCall", "Read"), call, result, item("toolCall", "Grep")])
        check(blocks.count == 3, "a send splits its tool run: \(blocks.map(\.kind))")
        guard blocks.count == 3, case .agent(let entry) = blocks[1].kind else { return }
        check(entry.agent.isSend && entry.result?.id == result.id && entry.failed, "the send keeps its failed result: \(entry)")
        if case .tools(let after) = blocks[2].kind { check(after.count == 1, "the result does not reappear in the next run") }
    }

    static func attachedFiles() {
        let notes = AttachedFile(name: "notes.txt", path: "/store/abc/notes.txt", bytes: 16)
        var withText = item("message", "user", text: "Read this", role: "user")
        withText.files = [notes]
        var fileOnly = item("message", "user", role: "user")
        fileOnly.files = [notes]
        let kinds = TranscriptBlock.build([withText, fileOnly, item("message", "user", role: "user")]).map(\.kind)
        check(kinds == [.user("Read this", files: [notes]), .user("", files: [notes])], "sent files stay with their message, even without text: \(kinds)")
    }

    static func usageLimitNotice() {
        var limit = item("notice", "Usage limit reached", text: "You've hit your session limit", status: "usage_limit")
        limit.resetsAt = "2026-10-21T11:00:00Z"
        let blocks = TranscriptBlock.build([limit, item("done", "done", text: "You've hit your session limit", status: "failed")]).map(\.kind)
        guard blocks.count == 1, case .notice(let text) = blocks[0] else {
            check(false, "a usage limit is one notice and its failed end shows nothing: \(blocks)")
            return
        }
        check(text.hasPrefix("You've hit your session limit Resets "), "the notice names when the limit resets: \(text)")
        check(item("notice", "API error", text: "Overloaded").noticeText == "Overloaded", "a notice without a reset is its text")
    }

    static func injectedInstructions() {
        let kinds = TranscriptBlock.build([
            item("instructions", "Instructions", text: "# AGENTS.md instructions for /work"),
            item("message", "developer", text: "<canon-recall>recalled</canon-recall>", role: "developer"),
            item("message", "system", text: "Base rules", role: "system"),
            item("message", "user", text: "Fix the build", role: "user"),
            item("instructions", "Instructions", text: "  "),
        ]).map(\.kind)
        check(kinds == [
            .instructions("# AGENTS.md instructions for /work"),
            .instructions("<canon-recall>recalled</canon-recall>"),
            .instructions("Base rules"),
            .user("Fix the build", files: []),
        ], "injected context is instructions, never the agent's reply: \(kinds)")
    }

    static func turnFooter() {
        var reply = item("message", "assistant", text: "Done", role: "assistant")
        reply = TimelineItem(id: reply.id, kind: reply.kind, role: reply.role, title: reply.title, text: reply.text, timestamp: "2026-10-23T10:09:00Z", callId: nil, status: nil, truncated: false, truncationReason: nil, bodyRef: nil)
        let duration = TimelineItem(id: "d", kind: "event", role: nil, title: "Turn duration", text: "3m13s", timestamp: "2026-10-23T10:10:00Z", callId: nil, status: nil, truncated: false, truncationReason: nil, bodyRef: nil)
        let blocks = TranscriptBlock.build([reply, duration])
        check(blocks.count == 1 && blocks[0].kind == .assistant("Done"), "the turn's duration joins the reply that ended it: \(blocks.map(\.kind))")
        check(blocks.first?.finished == "3m13s" && blocks.first?.timestamp == "2026-10-23T10:10:00Z", "the reply carries how long the turn took and when it finished")
        let afterTools = TranscriptBlock.build([reply, item("toolCall", "Bash"), duration])
        check(afterTools.count == 3 && afterTools[0].finished == nil, "a duration after tools does not attach to an earlier reply: \(afterTools.map(\.kind))")
        check(TurnClock.clock("3m13s") == "3:13" && TurnClock.clock("12s") == "0:12" && TurnClock.clock("1h2m3s") == "1:02:03", "durations read as a clock")
        check(TurnClock.clock("about a minute") == "about a minute", "an unparsed duration is shown as recorded")
        check(TurnClock.footer(duration: "3m13s", at: nil) == "Finished 3:13", "the footer names the duration")
        check(TurnClock.time("not a time") == nil && TurnClock.time("2026-10-23T10:10:00.123Z") != nil, "times parse with and without fractional seconds")
    }

    static func gaps() {
        let user = TranscriptBlock.Kind.user("Fix it", files: [])
        let reply = TranscriptBlock.Kind.assistant("Looking")
        let tools = TranscriptBlock.Kind.tools([])
        let peer = TranscriptBlock.Kind.agent(AgentEntry(item: item("message", "builder", text: "Ready", role: "peer"), agent: AgentMessage(direction: "in", kind: "peer", peer: "builder", summary: "Ready")))
        check(TranscriptBlock.gap(above: user, after: nil) == 0, "the first block sits on the top padding")
        let within = [TranscriptBlock.gap(above: tools, after: reply), TranscriptBlock.gap(above: reply, after: tools), TranscriptBlock.gap(above: reply, after: reply)]
        let opening = TranscriptBlock.gap(above: reply, after: user)
        let turn = [TranscriptBlock.gap(above: user, after: reply), TranscriptBlock.gap(above: peer, after: tools), TranscriptBlock.gap(above: user, after: user)]
        check(Set(within).count == 1, "the agent's text and tool runs within a turn share one gap: \(within)")
        check(within[0] < opening && opening < turn.min()!, "within a turn is tighter than after a message, which is tighter than before one: \(within[0]) \(opening) \(turn)")
    }

    static func main() {
        gaps()
        turnFooter()
        injectedInstructions()
        usageLimitNotice()
        injected()
        outgoingSend()
        attachedFiles()
        let blocks = TranscriptBlock.build([
            item("message", "user", text: "Start", role: "user"),
            item("reasoning", "Reasoning summary", text: "Plan the work"),
            item("toolCall", "Bash"),
            item("event", "Context compacted", text: "Summary of earlier work"),
            item("event", "Codex Voice ended", status: "completed"),
            item("event", "Codex Voice started"),
            item("event", "Turn duration", text: "12s"),
            item("context", "context"),
            item("goal", "goal", text: "{}"),
            item("message", "assistant", text: "Done", role: "assistant"),
        ])
        let kinds = blocks.map(\.kind)
        check(kinds.count == 7, "\(kinds)")
        check(kinds[0] == .user("Start", files: []), "user message")
        if case .tools(let items) = kinds[1] {
            check(items.map(\.kind) == ["reasoning", "toolCall"], "reasoning stays with its tool run")
        } else {
            check(false, "tool run expected")
        }
        check(kinds[2] == .notice("Context compacted: Summary of earlier work"), "an event with text is an expandable notice")
        check(kinds[3] == .annotation("Codex Voice ended · completed"), "an event status follows its title")
        check(kinds[4] == .annotation("Codex Voice started"), "an event without text is an annotation")
        check(kinds[5] == .annotation("Finished 0:12"), "a turn duration with no reply before it stands alone")
        check(kinds[6] == .assistant("Done"), "context and goal records are not shown")
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
