import CoreGraphics

struct TranscriptSlot: Identifiable, Equatable {
    let block: TranscriptBlock
    let gap: CGFloat
    var id: String { block.id }

    static func slots(_ blocks: [TranscriptBlock]) -> [TranscriptSlot] {
        blocks.indices.map { TranscriptSlot(block: blocks[$0], gap: TranscriptBlock.gap(above: blocks[$0].kind, after: $0 > 0 ? blocks[$0 - 1].kind : nil)) }
    }
}

// Handing SwiftUI the same array while settled rows are unchanged lets it skip comparing every lazy row on each update.
@MainActor
final class StableSlots {
    private var kept: [TranscriptSlot] = []

    func keep(_ fresh: ArraySlice<TranscriptSlot>) -> [TranscriptSlot] {
        if !kept.elementsEqual(fresh) { kept = Array(fresh) }
        return kept
    }
}
