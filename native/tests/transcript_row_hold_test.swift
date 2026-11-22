@testable import AgenthailHelpers
import Foundation

@main
struct TranscriptRowHoldTest {
    static func expect(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }

    @MainActor
    static func main() {
        // A page can land in a layout pass that leaves the content height unchanged, since rows not yet laid out are estimated.
        let hold = TranscriptRowHold()
        hold.arm("m120", above: 71, before: 39, fresh: true)
        let landed = hold.pass(TranscriptPass(leadingChanged: true, resting: true, following: false, content: 4388))
        expect(landed == "m120", "an older page landing without a resize restores the reader's row, landed \(String(describing: landed))")
        expect(hold.holding, "the landed row is held while the page settles")
        expect(hold.target(top: 39, offset: -32, content: 4388) == nil, "a row still at its pre-insert top is not corrected from that top")
        let target = hold.target(top: 4326, offset: -32, content: 4388)
        expect(target == 4255, "the row's new top puts the reader back where they were, target \(String(describing: target))")

        expect(hold.measured, "a row that reported its new top is measured, so it is corrected rather than scrolled to again")
        let again = hold.target(top: 4326, offset: 4294, content: 4388)
        expect(again == 4255, "a correction a newer restore displaced is given again while the row is still landing, target \(String(describing: again))")
        expect(hold.target(top: 4326, offset: 4255, content: 4388) == nil, "a row back in place needs no correction")
        expect(hold.target(top: 4326, offset: 4100, content: 4388) == nil && hold.id == nil, "a move away from a row that has settled is the reader's and ends the hold")

        let unarmed = TranscriptRowHold()
        expect(unarmed.pass(TranscriptPass(leadingChanged: true, resting: true, following: false, content: 4388)) == nil, "a page nobody asked a hold for lands nothing")

        let moving = TranscriptRowHold()
        moving.arm("m120", above: 71, before: 39, fresh: true)
        expect(moving.pass(TranscriptPass(leadingChanged: true, resting: false, following: false, content: 4388)) == nil, "a page that lands while the reader scrolls does not pull them back")

        let rowless = TranscriptRowHold()
        rowless.request(fresh: true)
        expect(rowless.pending, "a request with no row to hold is still pending, so a reader who scrolls while it loads is held where they stop")
        rowless.arm("m141", above: 20, before: 630, fresh: false)
        expect(rowless.pending && rowless.id == "m141", "the row found after the reader scrolls is armed for the same request")
        let rowlessLanded = rowless.pass(TranscriptPass(leadingChanged: true, resting: true, following: false, content: 9000))
        expect(rowlessLanded == "m141", "the page lands the row armed after the scroll, landed \(String(describing: rowlessLanded))")

        let unanchored = TranscriptRowHold()
        unanchored.request(fresh: true)
        expect(unanchored.pass(TranscriptPass(leadingChanged: true, resting: true, following: false, content: 9000)) == nil, "a page with no row armed lands nothing")
        expect(!unanchored.pending, "a page that lands with no row armed ends the request")

        let followed = TranscriptRowHold()
        followed.arm("m120", above: 71, before: 39, fresh: true)
        expect(followed.pass(TranscriptPass(leadingChanged: true, resting: true, following: true, content: 4388)) == nil, "a page that lands while the reader follows the live edge restores nothing")
        expect(!followed.pending && followed.id == nil, "a page that lands while following ends the request, pending \(followed.pending) row \(String(describing: followed.id))")
        let followedRowless = TranscriptRowHold()
        followedRowless.request(fresh: true)
        _ = followedRowless.pass(TranscriptPass(leadingChanged: true, resting: true, following: true, content: 4388))
        expect(!followedRowless.pending, "a page that lands while following ends a request with no row")

        let tops = TranscriptRowTops()
        tops.first = "first"
        for (id, top) in [("first", 24), ("tail", 68000)] as [(String, CGFloat)] {
            tops.appeared(id)
            tops.set(id, top: top)
        }
        expect(tops.leading(above: 1134) == nil, "a laid-out row starting below the visible area is not held, leading \(String(describing: tops.leading(above: 1134)))")
        tops.appeared("near")
        tops.set("near", top: 500)
        expect(tops.leading(above: 1134)?.id == "near", "the first laid-out row starting in the visible area is held")

        let waiting = TranscriptRowHold()
        waiting.arm("m120", above: 71, before: 39, fresh: true)
        waiting.settle(until: .now - .seconds(1))
        expect(waiting.pass(TranscriptPass(leadingChanged: true, resting: true, following: false, content: 4388)) == "m120", "settling a hold that has not landed leaves it armed for its page")
        waiting.settle(until: nil)
        expect(waiting.holding, "a landed hold with a restore still queued stays in force")
        waiting.settle(until: .now - .seconds(1))
        expect(!waiting.holding && !waiting.landed, "a landed hold ends once its settle window has passed")

        let replaced = TranscriptRowHold()
        replaced.arm("m120", above: 71, before: 39, fresh: true)
        _ = replaced.pass(TranscriptPass(leadingChanged: true, resting: true, following: false, content: 4388))
        replaced.request(fresh: true)
        expect(!replaced.holding && replaced.id == nil && replaced.pending, "a new request with no row to hold ends the previous page's hold and stays pending, holding \(replaced.holding) row \(String(describing: replaced.id))")

        let anchor = TranscriptRowAnchor()
        var anchorTops: [String: CGFloat] = ["r20": 34646, "r21": 35178]
        func step(_ offset: CGFloat, leading: String = "r20") -> TranscriptRowAnchor.Step {
            anchor.pass(offset: offset, range: -44...99999, top: { anchorTops[$0] }, leading: { (leading, anchorTops[leading] ?? 0, (anchorTops[leading] ?? 0) - offset) })
        }
        expect(step(34537) == .none && anchor.id == "r20", "a reader at rest anchors the leading row without moving, anchored \(String(describing: anchor.id))")
        anchorTops = ["r20": 34563, "r21": 35095]
        let shrunk = step(34537)
        expect(shrunk == .scroll(34454), "rows above the reader re-estimated 83 pt shorter move the offset with the row, step \(shrunk)")
        anchorTops = ["r20": 34543, "r21": 35075]
        let twice = step(34537)
        expect(twice == .scroll(34434), "a second move before the first correction lands is measured from where the reader saw the row, step \(twice)")
        expect(step(34200) == .none, "a scroll while a correction is still queued does not replace where the reader saw the row")
        expect(step(34434) == .none, "the queued correction lands")
        expect(step(34200) == .none && step(34200) == .none, "a scroll moves only the offset, so the anchor does not undo it")
        anchorTops["r20"] = 34643
        expect(step(34300) == .none, "a move the offset already followed, as a resize does, needs no correction")
        expect(step(36000, leading: "r21") == .none && anchor.id == "r21", "a reader who scrolls away anchors the row now in view, anchored \(String(describing: anchor.id))")
        anchor.release()
        expect(anchor.id == nil, "a released anchor holds nothing")

        let late = TranscriptRowAnchor()
        var lateTop: CGFloat = 34646
        let lateStep = { late.pass(offset: 34537, range: -44...99999, top: { _ in lateTop }, leading: { ("r20", lateTop, lateTop - 34537) }) }
        _ = lateStep()
        expect(lateStep() == .none && late.id == "r20", "a resize whose row has not reported yet keeps the row")
        lateTop = 34563
        let reported = lateStep()
        expect(reported == .scroll(34454), "a row that reports its move a turn after the resize is put back where the reader saw it, step \(reported)")

        let midResize = TranscriptRowAnchor()
        let moved = midResize.pass(offset: 34537, range: -44...99999, top: { _ in 34563 }, leading: { ("r20", 34646, 109) })
        expect(midResize.id == "r20" && moved == .scroll(34454), "a row picked mid-resize, after it moved, is put back where the reader saw it before the resize, step \(moved)")
        let compensated = TranscriptRowAnchor()
        let kept = compensated.pass(offset: 34454, range: -44...99999, top: { _ in 34563 }, leading: { ("r20", 34646, 109) })
        expect(kept == .none, "a row picked mid-resize whose offset already moved with it needs no correction, step \(kept)")

        let undone = TranscriptRowAnchor()
        var undoneTop: CGFloat = 34646
        let undoneStep = { (offset: CGFloat) in undone.pass(offset: offset, range: -44...99999, top: { _ in undoneTop }, leading: { ("r20", undoneTop, undoneTop - offset) }) }
        _ = undoneStep(34537)
        undoneTop = 34563
        expect(undoneStep(34537) == .scroll(34454), "a move queues a correction")
        undoneTop = 34646
        expect(undoneStep(34537) == .cancel, "a move undone before its correction runs drops the queued correction")
        expect(undoneStep(34300) == .none && undoneStep(34300) == .none, "a later scroll is where the reader now is, not something to undo")
        undoneTop = 34600
        let afterScroll = undoneStep(34300)
        expect(afterScroll == .scroll(34254), "a move after that scroll is measured from where the reader scrolled to, step \(afterScroll)")

        let clamped = TranscriptRowAnchor()
        var clampedTop: CGFloat = 400
        let clampedStep = { (offset: CGFloat) in clamped.pass(offset: offset, range: -44...99999, top: { _ in clampedTop }, leading: { ("r1", clampedTop, clampedTop - offset) }) }
        _ = clampedStep(40)
        clampedTop = 300
        let floor = clampedStep(40)
        expect(floor == .scroll(-44), "a correction past the top of the transcript stops at it, step \(floor)")
        expect(clampedStep(-44) == .none && clampedStep(-44) == .none, "a clamped correction that lands ends")

        let swapping = TranscriptRowAnchor()
        var replacedTops: [String: CGFloat] = ["r1": 1000, "r2": 1400]
        let replacedStep = { (offset: CGFloat) in swapping.pass(offset: offset, range: -44...99999, top: { replacedTops[$0] }, leading: { ("r2", replacedTops["r2"] ?? 0, (replacedTops["r2"] ?? 0) - offset) }) }
        _ = swapping.pass(offset: 900, range: -44...99999, top: { replacedTops[$0] }, leading: { ("r1", 1000, 100) })
        replacedTops["r1"] = 1020
        expect(replacedStep(900) == .scroll(920), "a move queues a correction")
        replacedTops["r1"] = nil
        let swapped = replacedStep(900)
        expect(swapped == .cancel && swapping.id == "r2", "a row that leaves before its correction runs drops the correction as another row is picked, step \(swapped)")

        let shrinking = TranscriptRowAnchor()
        var shrinkingTop: CGFloat = 1200
        let shrinkingStep = { (offset: CGFloat, upper: CGFloat) in shrinking.pass(offset: offset, range: -44...upper, top: { _ in shrinkingTop }, leading: { ("r1", shrinkingTop, shrinkingTop - offset) }) }
        _ = shrinkingStep(1000, 5000)
        shrinkingTop = 1300
        expect(shrinkingStep(1000, 5000) == .scroll(1100), "a move queues a correction")
        expect(shrinkingStep(1050, 1050) == .none, "a correction the shrunk range stops short lands where it stopped")
        shrinkingTop = 1330
        let rebased = shrinkingStep(1050, 2000)
        expect(rebased == .scroll(1080), "the next move is measured from where that correction landed, step \(rebased)")

        let reversed = TranscriptRowAnchor()
        var reversedTop: CGFloat = 1200
        let reversedStep = { (upper: CGFloat) in reversed.pass(offset: 1000, range: -44...upper, top: { _ in reversedTop }, leading: { ("r1", reversedTop, reversedTop - 1000) }) }
        _ = reversedStep(5000)
        reversedTop = 1300
        expect(reversedStep(5000) == .scroll(1100), "a move queues a correction")
        reversedTop = 1200
        let undoneAtEnd = reversedStep(1000)
        expect(undoneAtEnd == .cancel, "a move undone as the range shrinks to the offset drops the correction rather than scrolling back past the row, step \(undoneAtEnd)")

        let tall = TranscriptRowAnchor()
        var tallTop: CGFloat = 32000
        let tallStep = { tall.pass(offset: 34537, range: -44...99999, top: { _ in tallTop }, leading: { ("r9", tallTop, tallTop - 34537) }) }
        _ = tallStep()
        tallTop = 31900
        let tallMove = tallStep()
        expect(tallMove == .scroll(34437), "a row taller than the view, starting far above it, is still held, step \(tallMove)")

        let once = TranscriptRowHold()
        once.arm("m120", above: 71, before: 39, fresh: true)
        _ = once.pass(TranscriptPass(leadingChanged: true, resting: true, following: false, content: 4388))
        expect(once.pass(TranscriptPass(leadingChanged: true, resting: true, following: false, content: 8638)) == nil, "a hold lands once")
    }
}
