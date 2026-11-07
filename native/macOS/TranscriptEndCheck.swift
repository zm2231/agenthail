import SwiftUI

enum TranscriptEndCheck: Equatable {
    case done
    case retry
    case scroll

    // The geometry action decides an offset move a turn after the geometry changes, so an unhandled one waits one retry per check.
    static func decide(following: Bool, resting: Bool, geometry: ScrollGeometry, seen: ScrollGeometry?, deferred: Bool) -> TranscriptEndCheck {
        guard following else { return .done }
        guard resting else { return .retry }
        let unhandled = seen.map { $0.contentSize == geometry.contentSize && $0.contentOffset != geometry.contentOffset } ?? false
        if unhandled, !deferred { return .retry }
        return TranscriptEnd(geometry).distance > 2 ? .scroll : .done
    }
}
