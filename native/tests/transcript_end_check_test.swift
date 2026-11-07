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

    static func decide(following: Bool = true, resting: Bool = true, _ geometry: ScrollGeometry, seen: ScrollGeometry?, deferred: Bool = false) -> TranscriptEndCheck {
        TranscriptEndCheck.decide(following: following, resting: resting, geometry: geometry, seen: seen, deferred: deferred)
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
        print("ok   transcript_end_check_test")
    }
}
