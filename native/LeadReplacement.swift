import Foundation

struct LeadReplacement: Decodable, Hashable, Identifiable {
    let id: Int64
    let projectId: String
    let name: String
    let sessionId: String
    let previousSessionId: String
    let changedBy: String
    let reason: String
    let at: String
    /// The computer that recorded it, set where several computers' snapshots are merged.
    var hostID: String? = nil

    var listID: String { "\(hostID ?? "")/\(id)" }

    func detail(now: Date) -> String {
        let relative = RelativeDateTimeFormatter()
        relative.unitsStyle = .full
        let when = TimestampParser.date(at).map { relative.localizedString(for: $0, relativeTo: now) }
        return ["Replaced", when, "by \(changedBy),", "because \(reason)"].compactMap { $0 }.joined(separator: " ")
    }
}

/// Until is empty while the conversation still holds the name.
struct CoordinatorTenure: Codable, Hashable {
    let name: String
    let from: String
    var until: String? = nil
}
