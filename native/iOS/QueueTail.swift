import Foundation

struct QueueTail: Equatable {
    struct Row: Identifiable, Equatable {
        var item: QueueState
        var leavingSince: Date? = nil
        var awaitsTurnEnd = false

        var id: String { "queue-\(item.listID)" }
        var isDead: Bool { item.status == "dead" }
        var isOwn: Bool { (item.author ?? "person") == "person" && PeerEnvelope(item.message).sender == nil }
    }

    static let shownCount = 3
    static let leavesAfter: TimeInterval = 8
    private static let clockSkew: TimeInterval = 5

    let generation = UUID()
    private(set) var rows: [Row] = []
    private var dropped: Set<String> = []
    private(set) var problem: String?
    var expanded = false

    var shown: [Row] {
        if expanded { return rows }
        let ranked = rows.sorted { rank($0) != rank($1) ? rank($0) < rank($1) : $0.item.id > $1.item.id }
        let kept = Set(ranked.prefix(Self.shownCount).map(\.id))
        return rows.filter { kept.contains($0.id) }
    }
    var hiddenCount: Int { rows.count - shown.count }
    var lastShownID: String? { shown.last?.id }
    var shownIDs: [String] { shown.map(\.id) }
    var nextDeparture: Date? {
        rows.compactMap { $0.leavingSince }.min()?.addingTimeInterval(Self.leavesAfter)
    }

    static func shows(_ item: QueueState) -> Bool {
        !item.isHistorical && ["pending", "inflight", "dead"].contains(item.status) && ["", "message"].contains(item.operation ?? "")
    }

    private func rank(_ row: Row) -> Int {
        row.isDead ? 0 : row.isOwn ? 1 : 2
    }

    mutating func update(active: [QueueState], timeline: [TimelineItem], turnRunning: Bool = false, now: Date) {
        let listed = active.filter(Self.shows).sorted { $0.id < $1.id }
        let listedIDs = Set(listed.map(\.listID))
        dropped.formIntersection(listedIDs)
        let gone = rows.filter { !listedIDs.contains($0.item.listID) || dropped.contains($0.item.listID) }.map { row in
            var leaving = row
            leaving.leavingSince = row.awaitsTurnEnd ? now : row.leavingSince ?? now
            leaving.awaitsTurnEnd = row.awaitsTurnEnd && turnRunning
            return leaving
        }
        let current = listed.filter { !dropped.contains($0.listID) }.map { Row(item: $0) }
        var unmatched = timeline.filter { $0.role == "user" && ($0.kind == "message" || $0.kind == "attachment") }
        rows = (current + gone).sorted { $0.item.id < $1.item.id }.filter { row in
            if row.isDead && row.leavingSince == nil { return true }
            if let at = row.leavingSince, now.timeIntervalSince(at) >= Self.leavesAfter { return false }
            guard let index = unmatched.firstIndex(where: { Self.delivered(row.item, as: $0) }) else { return true }
            unmatched.remove(at: index)
            return false
        }
        if hiddenCount == 0 { expanded = false }
    }

    mutating func drop(_ row: Row) {
        dropped.insert(row.item.listID)
        rows.removeAll { $0.id == row.id }
    }

    mutating func steered(_ row: Row, now: Date) {
        dropped.insert(row.item.listID)
        guard let index = rows.firstIndex(where: { $0.id == row.id }) else { return }
        rows[index].leavingSince = now
        rows[index].awaitsTurnEnd = true
    }

    mutating func report(_ problem: String?, from generation: UUID) {
        if generation == self.generation { self.problem = problem }
    }

    static func delivered(_ queued: QueueState, as item: TimelineItem) -> Bool {
        if let raw = item.timestamp, let at = TimestampParser.date(raw), let sent = queued.queuedDate, at < sent.addingTimeInterval(-clockSkew) {
            return false
        }
        let text = PeerEnvelope(queued.message).body.trimmingCharacters(in: .whitespacesAndNewlines)
        let attachments = queued.attachments ?? []
        if item.kind == "attachment" {
            return text.isEmpty && attachments.contains { $0.mediaType.hasPrefix("image/") }
        }
        guard !text.isEmpty else { return false }
        return PeerEnvelope(item.text).body.trimmingCharacters(in: .whitespacesAndNewlines) == text
    }
}
