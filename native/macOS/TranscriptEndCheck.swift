import SwiftUI

enum TranscriptEndCheck: Equatable {
    case done
    case retry
    case scroll
    case clip

    // The geometry action decides an offset move a turn after the geometry changes, so an unhandled one waits one retry per check.
    // SwiftUI can report an anchor it never applied: the clip then sits short by exactly the last growth.
    static func decide(following: Bool, resting: Bool, geometry: ScrollGeometry, seen: ScrollGeometry?, deferred: Bool,
                       lastGrowth: CGFloat? = nil, clipDistance: @autoclosure () -> CGFloat? = nil, held: @autoclosure () -> Bool = false) -> TranscriptEndCheck {
        guard following else { return .done }
        guard resting else { return .retry }
        let unhandled = seen.map { $0.contentSize == geometry.contentSize && $0.contentOffset != geometry.contentOffset } ?? false
        if unhandled, !deferred { return .retry }
        if TranscriptEnd(geometry).distance > 2 { return .scroll }
        guard let growth = lastGrowth, growth > 2, let short = clipDistance(), abs(short - growth) <= 1, !held() else { return .done }
        return deferred ? .clip : .retry
    }
}
