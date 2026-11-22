import CoreGraphics
import Foundation

/// A lazy stack's document can end before rows it has laid out, leaving the newest row out of reach, so the newest rows are built eagerly.
struct TranscriptTail: Equatable {
    static let rows = 3
    static let slack = 4
    static let stranded = 30
    static let cap = 12

    private var transcript: String?
    private var lazy: Set<String> = []
    private var first: String?
    private(set) var crossed = 0

    static let latestSlack: CGFloat = 70

    static func latestHidden(_ end: CGRect?, in visible: CGRect, following: Bool) -> Bool {
        guard let end, visible.height > 0 else { return !following }
        return end.maxY > visible.maxY + latestSlack
    }

    static func opening(count: Int) -> Int {
        max(count - rows, 0)
    }

    func split(_ ids: [String], in transcript: String) -> Int {
        guard transcript == self.transcript else { return Self.opening(count: ids.count) }
        if let last = ids.lastIndex(where: lazy.contains) { return last + 1 }
        if let first, let index = ids.lastIndex(of: first) { return index }
        return Self.opening(count: ids.count)
    }

    /// Unbuilt lazy rows sit at estimated places, so at the end the eager rows reach the top of the view.
    /// Moving a row out of the lazy stack makes it re-estimate every unbuilt row above, so the tail grows only while the reader is at rest.
    mutating func cover(_ ids: [String], in transcript: String, rows: [String: CGRect], visible: CGRect, resting: Bool = true) -> Bool {
        guard resting, transcript == self.transcript, visible.height > 0 else { return false }
        let start = split(ids, in: transcript)
        guard start > 0, ids.count - start < Self.cap, let newest = ids.last.flatMap({ rows[$0] }), newest.minY < visible.maxY,
              let top = rows[ids[start]], top.minY > visible.minY else { return false }
        lazy.remove(ids[start - 1])
        first = ids[start - 1]
        return true
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

/// After a text size change the lazy stack re-estimates unbuilt rows; a bottom anchor during that moves the stack's origin.
struct TranscriptRescale: Equatable {
    static let quiet: TimeInterval = 0.1
    static let cap: TimeInterval = 3

    private var start: TimeInterval?
    private var moved: TimeInterval?
    private var frame: CGRect?

    var active: Bool { start != nil }

    mutating func begin(at now: TimeInterval) {
        start = now
        moved = now
    }

    mutating func stack(_ frame: CGRect, at now: TimeInterval) {
        if active, frame != self.frame { moved = now }
        self.frame = frame
    }

    mutating func settle(at now: TimeInterval) -> Bool {
        guard let start, let moved else { return false }
        guard now - moved >= Self.quiet || now - start >= Self.cap else { return false }
        self.start = nil
        self.moved = nil
        return true
    }
}

@MainActor
final class TranscriptTailLayout {
    var tail = TranscriptTail()
    var rows: [String: CGRect] = [:]
    var visible: CGRect = .zero
    var scrolling = false
    var newest: String?
    var end: CGRect?
    var rescale = TranscriptRescale()
}
