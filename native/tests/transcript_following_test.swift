@testable import AgenthailHelpers
import CoreGraphics
import Foundation

@main
struct TranscriptFollowingTest {
    static func expect(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }

    static let visible: CGFloat = 834

    static func pass(_ offset: CGFloat, _ end: CGFloat, after previous: CGFloat? = nil, resized: Bool = false, resting: Bool = false, scrollEnded: Bool = false) -> TranscriptFollowing.Pass {
        TranscriptFollowing.Pass(distance: max(end - offset - visible, 0), resized: resized, resizedThisTurn: resized, resting: resting,
                                 scrollEnded: scrollEnded, offsetMoved: previous.map { offset - $0 } ?? 0)
    }

    static func main() {
        // A reader scrolling to the end of a transcript, as a real window recorded it: each time they reached the end, a row joined the
        // eager tail and the lazy stack above it re-estimated its unbuilt rows, moving the end by thousands of points with the offset unchanged.
        let approach: [TranscriptFollowing.Pass] = [
            pass(100145, 102471),
            pass(101637, 102471, after: 100145),
            pass(101637, 138608, after: 101637, resized: true),
            pass(102545, 138608, after: 101637),
            pass(106945, 134879, after: 102545, resized: true),
            pass(108145, 110064, after: 106945, resized: true),
            pass(109230, 110064, after: 108145),
            pass(109230, 135971, after: 109230, resized: true),
            pass(133303, 135971, after: 109230),
            pass(135137, 135971, after: 133303),
            pass(135137, 138446, after: 135137, resized: true),
            pass(135545, 138446, after: 135137),
            pass(136745, 138446, after: 135545),
            pass(133733, 134567, after: 136745, resized: true, resting: true),
            pass(133733, 134567, scrollEnded: true),
        ]
        var following = false
        var flips = 0
        for step in approach {
            let next = TranscriptFollowing.next(following, step)
            if next != following { flips += 1 }
            following = next
        }
        expect(following && flips == 1, "a reader who scrolls to the end follows from the moment they reach it, however far the end is re-estimated while they scroll, flipped \(flips) times, following \(following)")

        var left = true
        left = TranscriptFollowing.next(left, pass(133683, 134567, after: 133733))
        expect(left, "a reader who scrolls up less than the slack keeps following")
        left = TranscriptFollowing.next(left, pass(133583, 134567, after: 133683))
        expect(!left, "a reader who scrolls up past the slack stops following")
        var grown = true
        grown = TranscriptFollowing.next(grown, pass(133733, 134567 + 3000, after: 133733, resized: true))
        grown = TranscriptFollowing.next(grown, pass(133733, 134567 + 3000, after: 133733))
        expect(grown, "content that grows below a following reader does not stop them following")
        expect(!TranscriptFollowing.next(false, pass(133000, 134567, after: 132000)), "a reader short of the end does not start following")
        expect(TranscriptFollowing.next(false, pass(133733, 134567, after: 133000)), "a reader who reaches the end starts following")
        expect(!TranscriptFollowing.next(false, pass(133000, 134567, scrollEnded: true)), "a reader who ends a scroll short of the end does not start following")
        // The same reader in a second recorded run: following turned on at an estimated end, the stack above re-estimated as the
        // scroll view's bottom anchor came on, and the gesture then ended thousands of points short of the new end.
        var ended = TranscriptFollowing.next(false, pass(101637, 102471, after: 100945))
        ended = TranscriptFollowing.next(ended, pass(101637, 139287, after: 101637, resized: true))
        ended = TranscriptFollowing.next(ended, pass(102145, 139287, after: 101637))
        ended = TranscriptFollowing.next(ended, TranscriptFollowing.Pass(distance: 139287 - 124545 - visible, scrollEnded: true, offsetMoved: 124545 - 98545))
        expect(ended, "a gesture that carried a following reader toward the end leaves them following, however far the end moved under them")
        expect(!TranscriptFollowing.next(true, TranscriptFollowing.Pass(distance: 400, scrollEnded: true, offsetMoved: -400)), "a gesture that carried a following reader away from the end stops them following")
        expect(!TranscriptFollowing.next(true, TranscriptFollowing.Pass(distance: 40, scrollEnded: true, offsetMoved: -40)), "a gesture that carries a following reader up short of the end stops them following, however little")

        var moved = true
        moved = TranscriptFollowing.next(moved, pass(130000, 134567, after: 129000))
        expect(moved, "a scroll the app makes toward the end, as a jump to latest or a scroll to the bottom does, does not stop a reader following")
        var animated = TranscriptFollowing.Pass(distance: 2000, animating: true, offsetMoved: -400)
        expect(TranscriptFollowing.next(true, animated), "a pass inside a jump's animation does not stop a reader following")
        animated.animating = false
        animated.resting = true
        animated.resizedThisTurn = true
        expect(TranscriptFollowing.next(true, animated), "an offset the lazy stack moves in the turn it resizes does not stop a reader following")
        expect(!TranscriptFollowing.next(true, pass(132000, 134567, after: 132400, resized: true)), "a reader who scrolls up in the pass that also grows the content stops following")
        var replaced = TranscriptFollowing.Pass(distance: 6502, resized: true, resizedThisTurn: true, offsetMoved: -6072)
        replaced.reset = true
        expect(TranscriptFollowing.next(true, replaced), "rows replaced under a following reader, which puts the offset back at the top, leave them following")
        var pulledBack = TranscriptFollowing.Pass(distance: 81, resized: true, resizedThisTurn: true, offsetMoved: -413)
        pulledBack.overshot = true
        expect(TranscriptFollowing.next(true, pulledBack), "a view pulled back to an end that moved up under it leaves a following reader following")
        var shrunk = true
        shrunk = TranscriptFollowing.next(shrunk, pass(133733, 133967, after: 133733, resized: true))
        shrunk = TranscriptFollowing.next(shrunk, pass(133133, 133967, after: 133733))
        expect(shrunk, "a row that collapses while a reader follows keeps them following when the offset is clamped back to the end")
        print("ok")
    }
}
