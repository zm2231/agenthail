import Foundation

struct ScopeRequest: Hashable, Comparable {
    let scope: String
    var projectID: String? = nil

    static func < (lhs: ScopeRequest, rhs: ScopeRequest) -> Bool {
        (lhs.scope, lhs.projectID ?? "") < (rhs.scope, rhs.projectID ?? "")
    }
}

struct ScopePages {
    struct Listing {
        var rows: [SessionState] = []
        var cursor: String?
        var finished = false
        var generation: UInt64 = 0
    }

    struct Read {
        let cursor: String?
        let generation: UInt64
    }

    private(set) var listings: [ScopeRequest: Listing] = [:]
    private(set) var found: [SessionState] = []

    var isEmpty: Bool { listings.isEmpty && found.isEmpty }

    func hasMore(_ request: ScopeRequest) -> Bool { listings[request]?.finished != true }

    func next(_ request: ScopeRequest) -> Read? {
        let listing = listings[request] ?? Listing()
        return listing.finished ? nil : Read(cursor: listing.cursor, generation: listing.generation)
    }

    mutating func append(_ page: ScopePage, to request: ScopeRequest, after read: Read) -> Bool {
        var listing = listings[request] ?? Listing()
        guard listing.generation == read.generation, listing.cursor == read.cursor, !listing.finished else { return false }
        listing.rows = Self.merged(listing.rows, page.sessions)
        listing.cursor = page.nextCursor
        listing.finished = page.nextCursor == nil
        listings[request] = listing
        return true
    }

    mutating func restart(_ request: ScopeRequest) -> (rows: Int, generation: UInt64)? {
        guard var listing = listings[request] else { return nil }
        listing.generation += 1
        listings[request] = listing
        return (listing.rows.count, listing.generation)
    }

    mutating func replace(_ request: ScopeRequest, rows: [SessionState], cursor: String?, generation: UInt64) -> Bool {
        guard var listing = listings[request], listing.generation == generation else { return false }
        listing.rows = Self.merged([], rows)
        listing.cursor = cursor
        listing.finished = cursor == nil
        listings[request] = listing
        return true
    }

    mutating func setFound(_ rows: [SessionState]) {
        found = Self.merged([], rows)
    }

    mutating func adopt(_ sessions: [SessionState]) {
        guard !isEmpty else { return }
        let fresh = Dictionary(sessions.map { ($0.id, $0) }, uniquingKeysWith: { _, last in last })
        listings = listings.mapValues { listing in
            var listing = listing
            listing.rows = listing.rows.map { fresh[$0.id] ?? $0 }
            return listing
        }
        found = found.map { fresh[$0.id] ?? $0 }
    }

    mutating func remove(_ id: String) -> Bool {
        var held = false
        listings = listings.mapValues { listing in
            var listing = listing
            if listing.rows.contains(where: { $0.id == id }) {
                listing.rows.removeAll { $0.id == id }
                held = true
            }
            return listing
        }
        if found.contains(where: { $0.id == id }) {
            found.removeAll { $0.id == id }
            held = true
        }
        return held
    }

    func joined(_ sessions: [SessionState]) -> [SessionState] {
        guard !isEmpty else { return sessions }
        var seen = Set(sessions.map(\.id))
        var result = sessions
        for row in listings.keys.sorted().flatMap({ listings[$0]?.rows ?? [] }) + found where seen.insert(row.id).inserted {
            result.append(row)
        }
        return result
    }

    private static func merged(_ held: [SessionState], _ page: [SessionState]) -> [SessionState] {
        var rows = held
        var index = Dictionary(rows.enumerated().map { ($1.id, $0) }, uniquingKeysWith: { first, _ in first })
        for row in page {
            if let at = index[row.id] { rows[at] = row } else {
                index[row.id] = rows.count
                rows.append(row)
            }
        }
        return rows
    }
}
