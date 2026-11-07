@testable import AgenthailHelpers
import Foundation
import SwiftUI

@main
struct TranscriptEndCheckTest {
    static func check(_ condition: Bool, _ message: String) {
        guard !condition else { return }
        print("FAIL: \(message)")
        exit(1)
    }

    static func geometry(offset: CGFloat, content: CGFloat = 1000) -> ScrollGeometry {
        ScrollGeometry(contentOffset: CGPoint(x: 0, y: offset), contentSize: CGSize(width: 400, height: content), contentInsets: EdgeInsets(), containerSize: CGSize(width: 400, height: 600))
    }

    nonisolated(unsafe) static var clipReads = 0
    nonisolated(unsafe) static var holdReads = 0

    static func read<Value>(_ value: Value, counting count: inout Int) -> Value {
        count += 1
        return value
    }

    static func decide(following: Bool = true, resting: Bool = true, _ geometry: ScrollGeometry, seen: ScrollGeometry?, deferred: Bool = false,
                       growth: CGFloat? = 44, clip: CGFloat? = nil, held: Bool = false) -> TranscriptEndCheck {
        TranscriptEndCheck.decide(following: following, resting: resting, geometry: geometry, seen: seen, deferred: deferred,
                                  lastGrowth: growth, clipDistance: read(clip, counting: &clipReads), held: read(held, counting: &holdReads))
    }

    static func main() {
        let end = geometry(offset: 400)
        let short = geometry(offset: 350)
        check(decide(following: false, short, seen: short) == .done, "a transcript that is not following is left where it is")
        check(decide(resting: false, short, seen: short) == .retry, "a check during a scroll waits for it to rest")
        check(decide(end, seen: end) == .done, "a followed transcript at the end needs nothing")
        check(decide(short, seen: short) == .scroll, "a followed transcript the action saw short of the end returns to it")
        check(decide(short, seen: end) == .retry, "an offset move the action has not handled waits one retry")
        check(decide(short, seen: end, deferred: true) == .scroll, "an offset move still unhandled at the retry returns to the end")
        check(decide(geometry(offset: 330), seen: end, deferred: true) == .scroll, "a new offset at the retry does not start another wait, so a stream of moves cannot starve the check")
        check(decide(geometry(offset: 380, content: 1100), seen: end) == .scroll, "content the action has not seen is caught up with at once")
        check(decide(short, seen: nil) == .scroll, "a check before any action runs on the geometry alone")

        check(decide(end, seen: end, clip: 44) == .retry, "a clip short by the last growth waits one retry")
        check(decide(end, seen: end, deferred: true, clip: 150) == .done, "a clip short by something other than the last growth is a reader's move and is left alone")
        check(decide(end, seen: end, deferred: true, growth: nil, clip: 44) == .done, "with no growth seen, a short clip is left alone")
        check(decide(end, seen: end, deferred: true, growth: -44, clip: 44) == .done, "content that shrank is never the missed anchor")
        check(decide(end, seen: end, deferred: true, clip: 44) == .clip, "a clip still short at the retry while SwiftUI reports the end is moved by AppKit")
        check(decide(end, seen: end, deferred: true, clip: 44, held: true) == .done, "a selection or a drag at the retry still holds the clip")
        check(decide(end, seen: end, clip: 2) == .done, "a clip within two points of the end is left alone")
        check(decide(end, seen: end, clip: nil) == .done, "with no clip to read, SwiftUI's report stands")
        check(decide(end, seen: end, clip: 44, held: true) == .done, "a text selection or a drag holds the clip where it is")
        check(decide(following: false, end, seen: end, clip: 44) == .done, "a transcript that is not following is never moved by AppKit")
        check(decide(resting: false, end, seen: end, clip: 44) == .retry, "a scroll under way is never moved by AppKit")
        check(decide(short, seen: short, clip: 44) == .scroll, "a shortfall SwiftUI sees is corrected through SwiftUI")
        clipReads = 0
        holdReads = 0
        _ = decide(short, seen: short, clip: 44)
        _ = decide(following: false, end, seen: end, clip: 44)
        _ = decide(resting: false, end, seen: end, clip: 44)
        check(clipReads == 0 && holdReads == 0, "the clip and the selection are read only when SwiftUI already reports the end, read \(clipReads) and \(holdReads) times")
        _ = decide(end, seen: end, clip: 1)
        check(clipReads == 1 && holdReads == 0, "the selection is read only for a clip that is short, read \(holdReads) times")
        print("ok   transcript_end_check_test")
    }
}
