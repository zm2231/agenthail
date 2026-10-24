import Foundation

/// The trailing rows of a transcript that are laid out; older rows join as the reader nears them.
struct TranscriptWindow: Equatable {
    static let opening = 24
    static let step = 24

    private var first: String?

    func start(in ids: [String]) -> Int {
        if let first, let index = ids.firstIndex(of: first) { return index }
        return max(ids.count - Self.opening, 0)
    }

    func shown<Row: Identifiable>(_ rows: [Row]) -> ArraySlice<Row> where Row.ID == String {
        rows[start(in: rows.map(\.id))...]
    }

    func hidesOlder(in ids: [String]) -> Bool {
        start(in: ids) > 0
    }

    mutating func pin(in ids: [String]) {
        guard first == nil, !ids.isEmpty else { return }
        first = ids[start(in: ids)]
    }

    mutating func revealOlder(in ids: [String]) {
        let start = start(in: ids)
        guard start > 0 else { return }
        first = ids[max(start - Self.step, 0)]
    }

    mutating func revealThrough(_ id: String, in ids: [String]) {
        guard let index = ids.firstIndex(of: id), index < start(in: ids) else { return }
        first = id
    }
}
