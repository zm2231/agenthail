@testable import AgenthailHelpers
import Foundation

@main
struct TranscriptBlockTest {
    static func item(_ kind: String, _ title: String, text: String = "", role: String? = nil, status: String? = nil) -> TimelineItem {
        TimelineItem(id: UUID().uuidString, kind: kind, role: role, title: title, text: text, timestamp: nil, callId: nil, status: status, truncated: false, truncationReason: nil, bodyRef: nil)
    }

    static func injected() {
        var peer = item("message", "builder", text: "Branch is ready", role: "peer")
        peer.sender = "builder"
        var anonymous = item("message", "Agent message", text: "Hello", role: "peer")
        anonymous.sender = ""
        let kinds = TranscriptBlock.build([
            peer,
            anonymous,
            item("event", "Task notification", text: "Tests passed", role: "system", status: "completed"),
        ]).map(\.kind)
        check(kinds == [
            .peer(sender: "builder", text: "Branch is ready"),
            .peer(sender: "another agent", text: "Hello"),
            .notice("Task notification · completed: Tests passed"),
        ], "injected prompts are not shown as the agent's own reply: \(kinds)")
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

    static func main() {
        usageLimitNotice()
        injected()
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
        check(kinds[5] == .annotation("Worked for 12s"), "turn duration")
        check(kinds[6] == .assistant("Done"), "context and goal records are not shown")
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
