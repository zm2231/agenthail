import CoreGraphics

/// A lazy stack's document can end before rows it has laid out, leaving the newest row out of reach, so the newest rows are built eagerly.
struct TranscriptTail: Equatable {
    static let rows = 3
    static let slack = 4

    private var transcript: String?
    private var first: String?
    private var lastLazy: String?

    static func opening(count: Int) -> Int {
        max(count - rows, 0)
    }

    func split(_ ids: [String], in transcript: String) -> Int {
        guard transcript == self.transcript else { return Self.opening(count: ids.count) }
        if let first, let index = ids.lastIndex(of: first) { return index }
        if let lastLazy, let index = ids.lastIndex(of: lastLazy) { return index + 1 }
        return Self.opening(count: ids.count)
    }

    mutating func update(_ ids: [String], in transcript: String, rows: [String: CGRect], visible: CGRect, following: Bool) {
        var start = split(ids, in: transcript)
        let target = Self.opening(count: ids.count)
        if following, target - start >= Self.slack {
            while start < target, let row = rows[ids[start]], row.maxY < visible.minY { start += 1 }
        }
        self.transcript = transcript
        first = start < ids.count ? ids[start] : nil
        lastLazy = start > 0 ? ids[start - 1] : nil
    }
}

@MainActor
final class TranscriptTailLayout {
    var tail = TranscriptTail()
    var rows: [String: CGRect] = [:]
    var visible: CGRect = .zero
}
