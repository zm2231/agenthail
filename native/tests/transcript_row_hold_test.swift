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
        let landed = hold.pass(TranscriptPass(leadingChanged: true, resting: true, content: 4388))
        expect(landed == "m120", "an older page landing without a resize restores the reader's row, landed \(String(describing: landed))")
        expect(hold.holding, "the landed row is held while the page settles")
        expect(hold.target(top: 39, offset: -32, content: 4388) == nil, "a row still at its pre-insert top is not corrected from that top")
        let target = hold.target(top: 4326, offset: -32, content: 4388)
        expect(target == 4255, "the row's new top puts the reader back where they were, target \(String(describing: target))")

        let unarmed = TranscriptRowHold()
        expect(unarmed.pass(TranscriptPass(leadingChanged: true, resting: true, content: 4388)) == nil, "a page nobody asked a hold for lands nothing")

        let moving = TranscriptRowHold()
        moving.arm("m120", above: 71, before: 39, fresh: true)
        expect(moving.pass(TranscriptPass(leadingChanged: true, resting: false, content: 4388)) == nil, "a page that lands while the reader scrolls does not pull them back")

        let once = TranscriptRowHold()
        once.arm("m120", above: 71, before: 39, fresh: true)
        _ = once.pass(TranscriptPass(leadingChanged: true, resting: true, content: 4388))
        expect(once.pass(TranscriptPass(leadingChanged: true, resting: true, content: 8638)) == nil, "a hold lands once")
    }
}
