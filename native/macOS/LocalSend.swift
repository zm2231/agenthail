import Foundation

struct LocalSend: Identifiable, Equatable {
    let id = UUID()
    let text: String
    let sentAt: Date
    var status: String?

    static let clockSkew: TimeInterval = 5

    static func label(for status: String?) -> String {
        switch status {
        case "queued": return "Queued"
        case "submitted": return "Submitted"
        default: return "Sent"
        }
    }

    static func reconcile(_ sends: [LocalSend], with items: [TimelineItem]) -> [LocalSend] {
        var candidates = items.filter { $0.kind == "message" && $0.role == "user" }
        return sends.filter { send in
            let text = send.text.trimmingCharacters(in: .whitespacesAndNewlines)
            guard let index = candidates.firstIndex(where: { item in
                guard PeerEnvelope(item.text).body.trimmingCharacters(in: .whitespacesAndNewlines) == text else { return false }
                guard let raw = item.timestamp, let at = SessionTree.parseTimestamp(raw) else { return true }
                return at >= send.sentAt.addingTimeInterval(-clockSkew)
            }) else { return true }
            candidates.remove(at: index)
            return false
        }
    }
}
