import CoreGraphics

/// The row a reader who is not following is looking at. Unbuilt rows above it are re-estimated as the transcript changes,
/// which moves it in the content without any scroll; a scroll moves only the offset, and a resize that already keeps it in place leaves it be.
@MainActor
final class TranscriptRowAnchor {
    enum Step: Equatable {
        case none
        case scroll(CGFloat)
        case cancel
    }

    private var row: (id: String, top: CGFloat, screen: CGFloat)?
    private var pending: CGFloat?

    var id: String? { row?.id }

    func release() {
        row = nil
        pending = nil
    }

    /// `leading` is the row in view with its top and where the reader saw it; mid-resize, as they saw it before the resize.
    func pass(offset: CGFloat, range: ClosedRange<CGFloat>, top: (String) -> CGFloat?, leading: () -> (id: String, top: CGFloat, screen: CGFloat)?) -> Step {
        if let pending, abs(offset - pending) <= 1 { self.pending = nil }
        if self.row.flatMap({ top($0.id) }) == nil { pick(leading) }
        guard let row, let now = top(row.id) else { return .none }
        guard abs(now - row.top) >= 1 else {
            if pending == nil, abs(now - offset - row.screen) >= 1 { pick(leading) }
            return .none
        }
        self.row?.top = now
        let target = min(max(now - row.screen, range.lowerBound), range.upperBound)
        guard abs(target - offset) >= 1 else {
            guard pending != nil else { return .none }
            pending = nil
            return .cancel
        }
        pending = target
        return .scroll(target)
    }

    private func pick(_ leading: () -> (id: String, top: CGFloat, screen: CGFloat)?) {
        pending = nil
        row = leading()
    }
}
