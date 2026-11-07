import CoreGraphics

struct TranscriptSlot: Identifiable, Equatable {
    let block: TranscriptBlock
    let gap: CGFloat
    let turn: String
    var id: String { block.id }

    static func slots(_ blocks: [TranscriptBlock]) -> [TranscriptSlot] {
        var turn = blocks.first?.id ?? ""
        return blocks.indices.map { index in
            let block = blocks[index]
            if block.kind.isMessageIn { turn = block.id }
            return TranscriptSlot(block: block, gap: TranscriptBlock.gap(above: block.kind, after: index > 0 ? blocks[index - 1].kind : nil), turn: turn)
        }
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
