import CoreGraphics

/// The row a reader who is not following is looking at. Unbuilt rows above it are re-estimated as the transcript changes,
/// which moves it in the content without any scroll; a scroll moves only the offset, and a resize that already keeps it in place leaves it be.
@MainActor
final class TranscriptRowAnchor {
    private var row: (id: String, top: CGFloat, screen: CGFloat, confirmed: Bool)?
    private var pending: CGFloat?

    var id: String? { row?.id }

    func release() {
        row = nil
        pending = nil
    }

    func pass(offset: CGFloat, visible: CGFloat, range: ClosedRange<CGFloat>, resized: Bool, top: (String) -> CGFloat?, leading: () -> (id: String, top: CGFloat)?) -> CGFloat? {
        guard let row, let now = top(row.id), now - offset < visible, now - offset > -visible else {
            self.row = leading().map { ($0.id, $0.top, $0.top - offset, !resized) }
            pending = nil
            return nil
        }
        // A row picked while the transcript was resizing can carry a top from before that layout, so its next report is taken as where it is.
        guard row.confirmed else {
            self.row = (row.id, now, now - offset, true)
            return nil
        }
        if let pending, abs(offset - pending) <= 1 { self.pending = nil }
        guard resized, abs(now - row.top) >= 1 else {
            self.row?.top = now
            if pending == nil { self.row?.screen = now - offset }
            return nil
        }
        self.row?.top = now
        let target = min(max(now - row.screen, range.lowerBound), range.upperBound)
        guard abs(target - offset) >= 1 else { return nil }
        pending = target
        return target
    }
}
