import Foundation

struct LocalSend: Identifiable, Equatable {
    let id = UUID()
    let idempotencyKey = UUID().uuidString
    let text: String
    var attachments: [ComposerAttachment] = []
    let sentAt: Date
    var status: String?
    var busyDelivery: String? = nil
    var turnSettings = TurnSettings()
    var failure: String? = nil

    static let clockSkew: TimeInterval = 5

    var displayText: String {
        guard !attachments.isEmpty else { return text }
        let names = "Attached: " + attachments.map(\.name).joined(separator: ", ")
        return text.isEmpty ? names : text + "\n\n" + names
    }

    static func label(for status: String?, waitingOnYou: Bool = false) -> String {
        switch status {
        case "queued": return waitingOnYou ? "Queued · Waiting on you" : "Queued"
        case "submitted": return "Submitted"
        default: return "Sent"
        }
    }

    // The daemon names attached files after the typed text, and an
    // image-only send records no user text at all, only its image.
    static func reconcile(_ sends: [LocalSend], with items: [TimelineItem]) -> [LocalSend] {
        var candidates = items.filter { ($0.kind == "message" || $0.kind == "attachment") && $0.role == "user" }
        return sends.filter { send in
            let text = send.text.trimmingCharacters(in: .whitespacesAndNewlines)
            guard let index = candidates.firstIndex(where: { item in
                guard matches(item, text: text, attached: !send.attachments.isEmpty) else { return false }
                guard let raw = item.timestamp, let at = SessionTree.parseTimestamp(raw) else { return true }
                return at >= send.sentAt.addingTimeInterval(-clockSkew)
            }) else { return true }
            candidates.remove(at: index)
            return false
        }
    }

    private static func matches(_ item: TimelineItem, text: String, attached: Bool) -> Bool {
        if item.kind == "attachment" { return attached && text.isEmpty }
        let body = PeerEnvelope(item.text).body.trimmingCharacters(in: .whitespacesAndNewlines)
        if body == text { return true }
        guard attached else { return false }
        return text.isEmpty ? body.hasPrefix(attachedFilePrefix) : body.hasPrefix(text + "\n\n" + attachedFilePrefix)
    }

    static let attachedFilePrefix = "Attached file: "
}
