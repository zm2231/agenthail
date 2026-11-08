@testable import AgenthailHelpers
import Foundation

@main
struct TranscriptUnknownKindTest {
    static func item(_ kind: String, _ title: String, text: String = "") -> TimelineItem {
        TimelineItem(id: UUID().uuidString, kind: kind, role: nil, title: title, text: text, timestamp: nil, callId: nil, status: nil, truncated: false, truncationReason: nil, bodyRef: nil)
    }

    static func main() {
        let toolRun = TranscriptBlock.build(["toolCall", "toolResult", "reasoning", "tool", "command"].map { item($0, $0) })
        check(toolRun.count == 1, "the dashboard's tool kinds stay in one tool run: \(toolRun.count)")
        if case .tools(let items) = toolRun.first?.kind {
            check(items.count == 5, "every tool kind is in the run")
        } else {
            check(false, "tool kinds make a tool run")
        }

        let blocks = TranscriptBlock.build([item("toolCall", "Run tests"), item("review", "Review summary", text: "**2** findings"), item("toolResult", "ok")])
        check(blocks.count == 3, "an unknown kind splits the tool run around its card: \(blocks.count)")
        guard blocks.count == 3, case .unknown(let title, let kind, let text) = blocks[1].kind else { return check(false, "an unknown kind gets its own card") }
        check(title == "Review summary" && kind == "review" && text == "**2** findings", "the card keeps the title, the kind and the text")

        if case .unknown(let title, let kind, _) = TranscriptBlock.build([item("mystery", "  ")]).first?.kind {
            check(title == "Unknown timeline item" && kind == "mystery", "a card without a title says so, as on the dashboard")
        } else {
            check(false, "an untitled unknown kind still gets a card")
        }
        if case .unknown(_, let kind, _) = TranscriptBlock.build([item("", "Empty kind")]).first?.kind {
            check(kind == "unknown", "an empty kind reads as unknown")
        } else {
            check(false, "an empty kind gets a card")
        }

        let skipped = TranscriptBlock.build(["context", "goal", "done", "phase"].map { item($0, $0) })
        check(skipped.isEmpty, "kinds shown elsewhere stay out of the transcript")
        print("ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
