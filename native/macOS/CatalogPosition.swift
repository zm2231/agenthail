import Foundation

struct CatalogPosition: Equatable {
    var epoch: String?
    var cursor: UInt64

    mutating func adopt(snapshotEpoch: String?, snapshotSeq: UInt64?) -> Bool {
        if snapshotEpoch != epoch {
            epoch = snapshotEpoch
            cursor = snapshotSeq ?? 0
            return true
        }
        cursor = max(cursor, snapshotSeq ?? 0)
        return false
    }

    mutating func accept(_ seq: UInt64) -> Bool {
        guard seq > cursor else { return false }
        cursor = seq
        return true
    }
}

func reconciledSelection(selected: String?, sessions: [SessionState]) -> String? {
    if let selected, sessions.contains(where: { $0.id == selected }) { return selected }
    return sessions.first(where: \.current)?.id ?? sessions.first?.id
}
