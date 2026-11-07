@testable import AgenthailHelpers
import CoreGraphics
import Foundation

@main
struct TranscriptBlockIDsTest {
    static func item(_ id: String, _ kind: String, text: String = "", role: String? = nil, agent: AgentMessage? = nil) -> TimelineItem {
        var value = TimelineItem(id: id, kind: kind, role: role, title: kind, text: text, timestamp: nil, callId: nil, status: nil, truncated: false, truncationReason: nil, bodyRef: nil)
        value.agent = agent
        return value
    }

    static func peer(_ id: String) -> TimelineItem {
        item(id, "message", text: "Branch is ready", role: "peer", agent: AgentMessage(direction: "in", kind: "peer", peer: "builder", summary: "Branch is ready"))
    }

    static func main() {
        let items = [
            item("a", "toolCall"),
            item("tools-a", "message", text: "a reply named like a tool run", role: "assistant"),
            item("u", "message", text: "Look\n[Image: shot.png]", role: "user"),
            item("u#images", "message", text: "a reply named like an image note", role: "assistant"),
            peer("p"),
            peer("q"),
            item("agents:p", "message", text: "a reply named like a fold", role: "assistant"),
        ]
        let blocks = TranscriptBlock.build(items)
        let ids = blocks.map(\.id)
        check(blocks.count == 7, "every item keeps its own block: \(blocks.map(\.kind))")
        check(Set(ids).count == ids.count, "derived block ids never meet a provider id: \(ids)")
        check(Set(TranscriptBlock.build(items, foldAgents: false).map(\.id)).count == 8, "unfolded blocks are distinct too")

        var tail = TranscriptTail()
        tail.update(ids, in: "s", rows: [:], visible: .zero, following: false)
        let split = tail.split(ids, in: "s")
        check(split == TranscriptTail.opening(count: ids.count), "the lazy rows end where they were set: \(split)")
        let grown = TranscriptBlock.build(items + [item("new", "message", text: "later", role: "assistant")]).map(\.id)
        check(tail.split(grown, in: "s") == split, "lazy membership holds when a row is added")
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
