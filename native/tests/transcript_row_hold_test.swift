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

        let once = TranscriptRowHold()
        once.arm("m120", above: 71, before: 39, fresh: true)
        _ = once.pass(TranscriptPass(leadingChanged: true, resting: true, following: false, content: 4388))
        expect(once.pass(TranscriptPass(leadingChanged: true, resting: true, following: false, content: 8638)) == nil, "a hold lands once")
    }
}
