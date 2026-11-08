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
        var held = moved
        held.update(long, in: "a", rows: rows(open..<141), visible: below, following: false) { _ in true }
        expect(held.split(long, in: "a") == 130, "a held selection or drag still lets rows above the viewport move, got \(held.split(long, in: "a"))")

        var reading = moved
        reading.update(long, in: "a", rows: rows(open..<141), visible: below, following: false)
        expect(reading.split(long, in: "a") == 130, "a reader who left the bottom has rows above the viewport moved, got \(reading.split(long, in: "a"))")
        expect(reading.crossed == 130 - open, "the update counts the rows that crossed, got \(reading.crossed)")
        reading.update(long, in: "a", rows: rows(open..<141), visible: below, following: false)
        expect(reading.crossed == 0, "a settled boundary moves no rows")

        moved.update(long, in: "a", rows: rows(open..<141), visible: below, following: true)
        expect(moved.split(long, in: "a") == 130, "the boundary passes rows wholly above the viewport and no others, got \(moved.split(long, in: "a"))")

        let few = CGRect(x: 0, y: CGFloat(open) * 100 + 200, width: 600, height: 900)
        var near = TranscriptTail()
        near.update(ids, in: "a", rows: rows(open..<100), visible: visible, following: true)
        let six = ids + (100..<106).map { "r\($0)" }
        near.update(six, in: "a", rows: rows(open..<106), visible: few, following: false)
        expect(near.split(six, in: "a") == open, "fewer out-of-view rows than the slack stay put")

        let tall = CGRect(x: 0, y: CGFloat(open) * 100, width: 600, height: 100_000)
        var stranded = TranscriptTail()
        stranded.update(ids, in: "a", rows: rows(open..<100), visible: visible, following: true)
        let short29 = ids + (100..<(100 + TranscriptTail.stranded - 1)).map { "r\($0)" }
        stranded.update(short29, in: "a", rows: rows(open..<short29.count), visible: tall, following: false)
        expect(stranded.split(short29, in: "a") == open, "visible rows stay eager below the stranded threshold")
        let past = ids + (100..<(100 + TranscriptTail.stranded)).map { "r\($0)" }
        var asked: [CGRect] = []
        stranded.update(past, in: "a", rows: rows(open..<past.count), visible: tall, following: false) { asked = $0; return true }
        expect(stranded.split(past, in: "a") == open, "a held selection keeps visible rows eager past the threshold")
        expect(asked == (open..<TranscriptTail.opening(count: past.count)).map { CGRect(x: 0, y: CGFloat($0) * 100, width: 600, height: 100) }, "the hold is asked about exactly the rows that would move, got \(asked.count)")
        stranded.update(past, in: "a", rows: rows(open..<past.count), visible: tall, following: false)
        expect(stranded.split(past, in: "a") == TranscriptTail.opening(count: past.count), "visible rows move once the stranded tail reaches the threshold")

        var unmeasured = TranscriptTail()
        unmeasured.update(ids, in: "a", rows: [:], visible: below, following: true)
        unmeasured.update(long, in: "a", rows: [:], visible: below, following: true)
        expect(unmeasured.split(long, in: "a") == open, "a row with no known frame keeps the boundary")

        var removing = TranscriptTail()
        removing.update(ids, in: "a", rows: [:], visible: visible, following: false)
        let boundary = removing.split(ids, in: "a")
        let withoutBoundary = ids.filter { $0 != ids[boundary] }
        expect(removing.split(withoutBoundary, in: "a") == boundary, "removing the first eager row keeps the row before it lazy")
        removing.update(withoutBoundary, in: "a", rows: [:], visible: visible, following: false)
        expect(removing.split(withoutBoundary, in: "a") == boundary, "the boundary moves to the row that took its place")
        let withoutBoth = withoutBoundary.filter { $0 != withoutBoundary[boundary - 1] && $0 != withoutBoundary[boundary] }
        expect(removing.split(withoutBoth, in: "a") == boundary - 1, "removing the first eager row and the lazy row before it in one update keeps the rows above lazy")
        let tailOnly = withoutBoundary.filter { !withoutBoundary[..<boundary].contains($0) }
        expect(removing.split(tailOnly, in: "a") == 0, "with every lazy row gone the surviving eager rows stay eager")

        var short = TranscriptTail()
        short.update(["s0", "s1"], in: "a", rows: [:], visible: visible, following: true)
        let grownShort = ["s0", "s1"] + (2..<10).map { "s\($0)" }
        expect(short.split(grownShort, in: "a") == 0, "a short transcript that grows keeps its rows eager until they scroll away")

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
