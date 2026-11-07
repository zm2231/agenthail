@testable import AgenthailHelpers
import Foundation

@main
struct TranscriptSlotsTest {
    @MainActor static func main() {
        func blocks(_ texts: [String]) -> [TranscriptBlock] {
            texts.enumerated().map { TranscriptBlock(id: "b\($0.offset)", kind: .assistant($0.element)) }
        }
        func base(_ slots: [TranscriptSlot]) -> UnsafeRawPointer? {
            slots.withUnsafeBufferPointer { UnsafeRawPointer($0.baseAddress) }
        }
        let stable = StableSlots()
        let first = stable.keep(TranscriptSlot.slots(blocks(["a", "b", "c", "d"])).prefix(3))
        let again = stable.keep(TranscriptSlot.slots(blocks(["a", "b", "c", "d", "e"])).prefix(3))
        expect(base(first) == base(again), "unchanged settled rows hand back the same array")

        let edited = stable.keep(TranscriptSlot.slots(blocks(["a", "B", "c", "d"])).prefix(3))
        expect(edited.map(\.block) == blocks(["a", "B", "c"]), "a settled row that changes is shown changed")
        expect(base(edited) != base(again), "a changed settled row hands back a new array")

        let moved = stable.keep(TranscriptSlot.slots(blocks(["a", "B", "c", "d"])).prefix(2))
        expect(moved.map(\.id) == ["b0", "b1"], "a moved boundary hands back the new settled rows")

        let older = TranscriptSlot.slots([TranscriptBlock(id: "u", kind: .user("q", files: []))] + blocks(["a"]))
        expect(older[1].gap == TranscriptBlock.gap(above: .assistant("a"), after: .user("q", files: [])), "a row's gap follows the row above it")
        expect(older[0].gap == 0, "the first row has no gap")

        let peer = TranscriptBlock(id: "p", kind: .agentFold(AgentFoldEntry(label: "1 message", senders: "builder", members: [])))
        let turns = TranscriptSlot.slots(blocks(["a"]) + [TranscriptBlock(id: "u", kind: .user("q", files: []))] + blocks(["b", "c"]) + [peer] + blocks(["d"]))
        expect(turns.map(\.turn) == ["b0", "u", "u", "u", "p", "p"], "each row belongs to the turn its latest incoming message opened, got \(turns.map(\.turn))")
    }

    static func expect(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
