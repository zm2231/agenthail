import SwiftUI

// Rows above can re-estimate after the scroll lands and move the row, so its live top is followed.
struct RevealedRow {
    static let quiet: Duration = .milliseconds(500)
    static let longest: Duration = .seconds(3)

    private let top: Binding<CGFloat>
    private var quietUntil: ContinuousClock.Instant
    private let giveUp: ContinuousClock.Instant

    init(top: Binding<CGFloat>) {
        self.top = top
        quietUntil = .now + Self.quiet
        giveUp = .now + Self.longest
    }

    var expired: Bool {
        let now = ContinuousClock.now
        return now >= quietUntil || now >= giveUp
    }

    mutating func target(in geometry: ScrollGeometry, resized: Bool) -> CGFloat? {
        if resized { quietUntil = .now + Self.quiet }
        let end = max(geometry.contentSize.height + geometry.contentInsets.bottom - geometry.containerSize.height + geometry.contentInsets.top, 0)
        let target = min(top.wrappedValue, end)
        return abs(geometry.contentOffset.y + geometry.contentInsets.top - target) > 1 ? target : nil
    }
}
