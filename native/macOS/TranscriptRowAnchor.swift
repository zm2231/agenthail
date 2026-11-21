import CoreGraphics

/// The row a reader who is not following is looking at. Unbuilt rows above it are re-estimated as the transcript changes,
/// which moves it in the content without any scroll, so the offset follows its content top; a scroll moves only the offset.
@MainActor
final class TranscriptRowAnchor {
    private var row: (id: String, top: CGFloat)?
    private var pending: CGFloat?

    var id: String? { row?.id }

    func release() {
        row = nil
        pending = nil
    }

    func pass(offset: CGFloat, visible: CGFloat, top: (String) -> CGFloat?, leading: () -> (id: String, top: CGFloat)?) -> CGFloat? {
        guard let row, let now = top(row.id), now - offset < visible, now - offset > -visible else {
            self.row = leading()
            pending = nil
            return nil
        }
        let moved = now - row.top
        self.row?.top = now
        guard abs(moved) >= 1 else { return nil }
        let target = (pending ?? offset) + moved
        pending = target
        return target
    }

    func applied() { pending = nil }
}
