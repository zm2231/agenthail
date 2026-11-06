@testable import AgenthailHelpers
import CoreGraphics
import Foundation

@main
struct TranscriptTailTest {
    static func main() {
        expect(TranscriptTail.opening(count: 100) == 100 - TranscriptTail.rows, "a long transcript opens with the fixed row count")
        expect(TranscriptTail.opening(count: 2) == 0, "a short transcript is all eager")
        expect(TranscriptTail.opening(count: 0) == 0, "an empty transcript has no rows")

        let ids = (0..<100).map { "r\($0)" }
        let open = 100 - TranscriptTail.rows
        func rows(_ range: Range<Int>) -> [String: CGRect] {
            range.reduce(into: [:]) { $0["r\($1)"] = CGRect(x: 0, y: CGFloat($1) * 100, width: 600, height: 100) }
        }
        let visible = CGRect(x: 0, y: 9_100, width: 600, height: 900)
        var tail = TranscriptTail()
        expect(tail.split(ids, in: "a") == open, "an unseen transcript opens by the rule")

        tail.update(ids, in: "a", rows: rows(open..<100), visible: visible, following: true)
        let grown = ids + ["r100"]
        expect(tail.split(grown, in: "a") == open, "a new row does not move the boundary")
        tail.update(grown, in: "a", rows: rows(open..<101), visible: visible, following: true)
        expect(tail.split(grown, in: "a") == open, "the boundary stays within its slack")

        expect(tail.split(grown, in: "b") == open + 1, "another transcript does not use this boundary")

        let older = ["o0", "o1"] + grown
        expect(tail.split(older, in: "a") == open + 2, "earlier rows do not change which row starts the tail")

        var long = (0..<140).map { "r\($0)" }
        var moved = TranscriptTail()
        moved.update(ids, in: "a", rows: rows(open..<100), visible: visible, following: true)
        long += ["r140"]
        let below = CGRect(x: 0, y: 13_100, width: 600, height: 900)
        moved.update(long, in: "a", rows: rows(open..<141), visible: below, following: false)
        expect(moved.split(long, in: "a") == open, "a reader who left the bottom keeps the boundary")

        moved.update(long, in: "a", rows: rows(open..<141), visible: below, following: true)
        expect(moved.split(long, in: "a") == 130, "the boundary passes rows wholly above the viewport and no others, got \(moved.split(long, in: "a"))")

        var unmeasured = TranscriptTail()
        unmeasured.update(ids, in: "a", rows: [:], visible: below, following: true)
        unmeasured.update(long, in: "a", rows: [:], visible: below, following: true)
        expect(unmeasured.split(long, in: "a") == open, "a row with no known frame keeps the boundary")

        var empty = TranscriptTail()
        empty.update([], in: "a", rows: [:], visible: visible, following: true)
        expect(empty.split(ids, in: "a") == open, "an empty transcript holds no boundary, so its first rows open by the rule")
    }

    static func expect(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
