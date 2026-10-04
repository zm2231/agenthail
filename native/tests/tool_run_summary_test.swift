import Foundation

@main
struct ToolRunSummaryTest {
    static func item(_ kind: String, _ title: String, status: String? = nil, text: String = "") -> TimelineItem {
        TimelineItem(id: UUID().uuidString, kind: kind, role: nil, title: title, text: text, timestamp: nil, callId: nil, status: status, truncated: false, bodyRef: nil)
    }

    static func main() {
        let mixed = [item("toolCall", "Bash"), item("toolResult", "Tool result"), item("toolCall", "Bash"), item("toolCall", "Read"), item("toolCall", "Bash"), item("toolCall", "Read"), item("toolCall", "Edit"), item("toolCall", "mystery")]
        expect(ToolRunSummary.label(mixed) == "Ran 3 commands and read 2 files +2 other", ToolRunSummary.label(mixed))
        expect(ToolRunSummary.label([item("toolCall", "Edit")]) == "Edited 1 file", "single category")
        expect(ToolRunSummary.label([item("reasoning", "Thinking")]) == "Thought", "reasoning only")
        expect(ToolRunSummary.label([item("toolCall", "Web search")]) == "Made 1 web lookup", "Codex web search")
        expect(ToolRunSummary.isFailure(item("toolResult", "Tool result", status: "error")), "error result is a failure")
        let mcp = [item("toolCall", "mcp__agent-hands__press_key"), item("toolCall", "mcp__agent-hands__list_apps"), item("toolCall", "Bash")]
        expect(ToolRunSummary.label(mcp) == "Used 2 agent-hands tools and ran 1 command", ToolRunSummary.label(mcp))
        print("tool run summary tests passed")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
