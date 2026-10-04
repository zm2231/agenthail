import Foundation

@main
struct ToolRunSummaryTest {
    static func item(_ kind: String, _ title: String, status: String? = nil, text: String = "", callId: String? = nil) -> TimelineItem {
        TimelineItem(id: UUID().uuidString, kind: kind, role: nil, title: title, text: text, timestamp: nil, callId: callId, status: status, truncated: false, truncationReason: nil, bodyRef: nil)
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
        let run = [item("toolCall", "Bash", callId: "a"), item("toolCall", "Read", callId: "b"), item("reasoning", "Thinking"), item("toolResult", "Tool result", text: "read", callId: "b"), item("toolResult", "Tool result", status: "error", text: "boom", callId: "a"), item("toolResult", "Tool result", text: "earlier page", callId: "z"), item("toolCall", "Edit", callId: "c")]
        let entries = ToolRunSummary.entries(run + [item("reasoning", "Thinking", text: "Weigh the options")])
        expect(entries.map(describe) == ["Bash:boom", "Read:read", "output:earlier page", "Edit:", "reasoning:Weigh the options"], entries.map(describe).joined(separator: ","))
        let long = (1...40).map { "line \($0)" }.joined(separator: "\n")
        expect(ToolRunSummary.outputPreview(long).split(separator: "\n").count == 24, "preview keeps the first 24 lines")
        expect(ToolRunSummary.outputPreview(String(repeating: "x", count: 5000)).count == 3000, "preview caps characters")
        expect(ToolRunSummary.outputPreview("short") == "short", "short output is its own preview")
        print("tool run summary tests passed")
    }

    static func describe(_ entry: ToolRunEntry) -> String {
        switch entry {
        case .call(let call, let results): "\(call.title):\(results.map(\.text).joined(separator: "|"))"
        case .output(let item): "output:\(item.text)"
        case .reasoning(let item): "reasoning:\(item.text)"
        }
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
