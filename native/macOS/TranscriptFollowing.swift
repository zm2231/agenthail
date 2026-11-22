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
        return following ? pass.distance < bottomSlack : pass.distance < readerReach
    }
}
