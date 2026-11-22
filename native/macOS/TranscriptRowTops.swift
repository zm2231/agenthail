import SwiftUI

// A reference, so a row reporting its place does not redraw the transcript.
@MainActor
final class TranscriptRowTops {
    private var tops: [String: CGFloat] = [:]
    private var earlier: [String: (top: CGFloat?, turn: Int)] = [:]
    private var shown: [String: Int] = [:]
    var moved: ((String) -> Void)?
    var first: String?
    var holdingOlder: (() -> Void)?

    func set(_ id: String, top: CGFloat) {
        if earlier[id]?.turn != RunLoopTurn.current { earlier[id] = (tops[id], RunLoopTurn.current) }
        tops[id] = top
        moved?(id)
    }

    // A lazy stack can build a row's replacement before the old one disappears, so a row is laid out while any copy of it is.
    func appeared(_ id: String) {
        shown[id, default: 0] += 1
        moved?(id)
    }

    func disappeared(_ id: String) {
        shown[id] = max((shown[id] ?? 0) - 1, 0)
        moved?(id)
    }

    func top(_ id: String) -> CGFloat? { (shown[id] ?? 0) > 0 ? tops[id] : nil }

    // An older page that continues the first block re-keys it, and a row starting below the visible area has estimated rows between it and the reader.
    func leading(above bottom: CGFloat) -> (id: String, top: CGFloat)? {
        tops.filter { (shown[$0.key] ?? 0) > 0 && $0.key != first && $0.value < bottom }.min { $0.value < $1.value }.map { ($0.key, $0.value) }
    }

    // A row taller than the view starts above it, so with no row starting in view the one across its top is used.
    func inView(from top: CGFloat, to bottom: CGFloat, beforeTurn: Bool = false) -> (id: String, top: CGFloat)? {
        let placed = beforeTurn ? Dictionary(uniqueKeysWithValues: tops.keys.compactMap { id in before(id).map { (id, $0) } }) : tops
        let laidOut = placed.filter { (shown[$0.key] ?? 0) > 0 && $0.key != first && $0.value < bottom }
        let starting = laidOut.filter { $0.value >= top }.min { $0.value < $1.value }
        return (starting ?? laidOut.max { $0.value < $1.value }).map { ($0.key, $0.value) }
    }

    // Mid-resize the reader still sees the rows where they were before this run loop turn's reports.
    private func before(_ id: String) -> CGFloat? {
        guard let earlier = earlier[id], earlier.turn == RunLoopTurn.current else { return tops[id] }
        return earlier.top
    }

    func holdOlderPlace() { holdingOlder?() }
}

extension View {
    func reportsTop(_ id: String, to tops: TranscriptRowTops?) -> some View {
        onGeometryChange(for: CGFloat.self) { $0.frame(in: .named(TranscriptRows.contentSpace)).minY } action: { tops?.set(id, top: $0) }
            .onAppear { tops?.appeared(id) }
            .onDisappear { tops?.disappeared(id) }
    }
}
