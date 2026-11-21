import CoreGraphics

struct TranscriptPass {
    var leadingChanged: Bool
    var resting: Bool
    var following: Bool
    var content: CGFloat
}

/// The reader's row from the moment they ask for an older page until it lands and settles.
@MainActor
final class TranscriptRowHold {
    private var row: (id: String, above: CGFloat, before: CGFloat, until: ContinuousClock.Instant, content: CGFloat?, top: CGFloat?, landing: Bool)?
    private var olderUntil: ContinuousClock.Instant?

    var pending: Bool { olderUntil.map { ContinuousClock.now < $0 } ?? false }
    var holding: Bool { row.map { $0.content != nil && ContinuousClock.now < $0.until } ?? false }
    var id: String? { row?.id }
    var before: CGFloat? { row?.before }
    var measured: Bool { row?.top != nil }

    func request(fresh: Bool) {
        if fresh || !pending { olderUntil = .now + .seconds(30) }
    }

    func arm(_ id: String, above: CGFloat, before: CGFloat, fresh: Bool) {
        request(fresh: fresh)
        row = (id, above, before, olderUntil ?? .now, nil, nil, true)
    }

    func release() { row = nil }

    func settle(until: ContinuousClock.Instant?) {
        guard row?.content != nil else { return }
        row?.until = until ?? .now + .seconds(86_400)
    }

    func forget() {
        olderUntil = nil
        row = nil
    }

    /// The row to restore when this pass lands an armed hold.
    func pass(_ pass: TranscriptPass) -> String? {
        guard pass.leadingChanged else { return nil }
        guard pass.resting, !pass.following else {
            forget()
            return nil
        }
        guard let row else {
            olderUntil = nil
            return nil
        }
        guard row.content == nil, ContinuousClock.now < row.until else { return nil }
        olderUntil = nil
        self.row?.content = pass.content
        self.row?.until = .now + .seconds(2)
        return row.id
    }

    /// Whether a landed hold is in force; an expired one is cleared.
    var landed: Bool {
        if let row, ContinuousClock.now >= row.until { self.row = nil }
        return row?.content != nil
    }

    /// The offset that puts the held row back, given its reported top. The row keeps its pre-insert top until it is laid out again.
    func target(top: CGFloat?, offset: CGFloat, content: CGFloat) -> CGFloat? {
        guard let row, row.content != nil, let top, top != row.before else { return nil }
        let target = max(top - row.above, 0)
        if content != row.content || top != row.top {
            self.row?.content = content
            self.row?.top = top
            self.row?.landing = abs(offset - target) > 1
            return self.row?.landing == true ? target : nil
        }
        if abs(offset - target) <= 1 {
            self.row?.landing = false
        } else if row.landing {
            return target
        } else {
            self.row = nil
        }
        return nil
    }
}
