import CoreGraphics

/// A lazy stack's document can end before rows it has laid out, leaving the newest row out of reach, so the newest rows are built eagerly.
struct TranscriptTail: Equatable {
    static let rows = 3
    static let slack = 4
    static let stranded = 30

    private var transcript: String?
    private var lazy: Set<String> = []
    private var first: String?
    private(set) var crossed = 0

    static func opening(count: Int) -> Int {
        max(count - rows, 0)
    }

    func split(_ ids: [String], in transcript: String) -> Int {
        guard transcript == self.transcript else { return Self.opening(count: ids.count) }
        if let last = ids.lastIndex(where: lazy.contains) { return last + 1 }
        if let first, let index = ids.lastIndex(of: first) { return index }
        return Self.opening(count: ids.count)
    }

    mutating func update(_ ids: [String], in transcript: String, rows: [String: CGRect], visible: CGRect, following: Bool, held: ([CGRect]) -> Bool = { _ in false }) {
        let before = split(ids, in: transcript)
        var start = before
        let target = Self.opening(count: ids.count)
        if target - start >= Self.slack {
            if following {
                while start < target, let row = rows[ids[start]], row.maxY < visible.minY { start += 1 }
            } else {
                var next = start
                while next < target, let row = rows[ids[next]], row.maxY < visible.minY || row.minY > visible.maxY { next += 1 }
                if next - start < Self.slack { next = start }
                if target - next >= Self.stranded, !held(ids[next..<target].compactMap { rows[$0] }) { next = target }
                start = next
            }
        }
        crossed = transcript == self.transcript ? start - before : 0
        self.transcript = transcript
        lazy = Set(ids[..<start])
        first = start < ids.count ? ids[start] : nil
    }
}

@MainActor
final class TranscriptTailLayout {
    var tail = TranscriptTail()
    var rows: [String: CGRect] = [:]
    var visible: CGRect = .zero
    var newest: String?
}
