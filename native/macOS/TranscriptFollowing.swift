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
    }

    static let bottomSlack: CGFloat = 70
    static let readerReach: CGFloat = 4

    static func next(_ following: Bool, _ pass: Pass) -> Bool {
        if pass.scrollEnded { return pass.distance < readerReach }
        guard !pass.resized else {
            return following || (pass.resting && !pass.inserted && pass.distance < readerReach)
        }
        if following, pass.resting, pass.resizedThisTurn, pass.distance >= bottomSlack { return following }
        if pass.animating { return following }
        // The end moves when the lazy stack re-estimates rows it has not built; only the reader's own move away from it stops them following.
        return following ? !(pass.offsetMoved < 0 && pass.distance >= bottomSlack) : pass.distance < readerReach
    }
}
