import CoreGraphics

enum TranscriptFollowing {
    struct Pass: Equatable {
        var distance: CGFloat
        var resized = false
        var resizedThisTurn = false
        var resting = true
        var animating = false
        var inserted = false
        var scrollEnded = false
        var offsetMoved: CGFloat = 0
        var reset = false
        var overshot = false
    }

    static let bottomSlack: CGFloat = 70
    static let readerReach: CGFloat = 4

    static func next(_ following: Bool, _ pass: Pass) -> Bool {
        // A reader's own gesture keeps them following only if it did not carry them up and away from the end.
        if pass.scrollEnded { return following ? !(pass.offsetMoved < 0 && pass.distance >= readerReach) : pass.distance < readerReach }
        guard !pass.resized else {
            // Replacing the rows puts the offset back at the top, which is not the reader leaving.
            if following { return pass.reset || !leftTheEnd(pass) }
            return pass.resting && !pass.inserted && pass.distance < readerReach
        }
        if following, pass.resting, pass.resizedThisTurn, pass.distance >= bottomSlack { return following }
        if pass.animating { return following }
        // The end moves when the lazy stack re-estimates rows it has not built; only the reader's own move away from it stops them following.
        return following ? !leftTheEnd(pass) : pass.distance < readerReach
    }

    // A view left past an end that moved up is pulled back to it; that move up is not the reader leaving.
    private static func leftTheEnd(_ pass: Pass) -> Bool {
        !pass.overshot && pass.offsetMoved < 0 && pass.distance >= bottomSlack
    }
}
