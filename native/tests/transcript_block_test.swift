import Foundation

@main
struct TranscriptBlockTest {
    static func item(_ kind: String, _ title: String, text: String = "", role: String? = nil, status: String? = nil) -> TimelineItem {
        TimelineItem(id: UUID().uuidString, kind: kind, role: role, title: title, text: text, timestamp: nil, callId: nil, status: status, truncated: false, truncationReason: nil, bodyRef: nil)
    }

    static func main() {
        let blocks = TranscriptBlock.build([
            item("message", "user", text: "Start", role: "user"),
            item("reasoning", "Reasoning summary", text: "Plan the work"),
            item("toolCall", "Bash"),
            item("event", "Context compacted", text: "Summary of earlier work"),
            item("event", "Codex Voice ended", status: "completed"),
            item("event", "Codex Voice started"),
            item("event", "Turn duration", text: "12s"),
            item("context", "context"),
            item("message", "assistant", text: "Done", role: "assistant"),
        ])
        let kinds = blocks.map(\.kind)
        check(kinds.count == 7, "\(kinds)")
        check(kinds[0] == .user("Start"), "user message")
        if case .tools(let items) = kinds[1] {
            check(items.map(\.kind) == ["reasoning", "toolCall"], "reasoning stays with its tool run")
        } else {
            check(false, "tool run expected")
        }
        check(kinds[2] == .notice("Context compacted: Summary of earlier work"), "an event with text is an expandable notice")
        check(kinds[3] == .annotation("Codex Voice ended · completed"), "an event status follows its title")
        check(kinds[4] == .annotation("Codex Voice started"), "an event without text is an annotation")
        check(kinds[5] == .annotation("Worked for 12s"), "turn duration")
        check(kinds[6] == .assistant("Done"), "context records are not shown")
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
