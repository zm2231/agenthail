import Foundation

struct LocalSend: Identifiable, Equatable {
    let id = UUID()
    let idempotencyKey = UUID().uuidString
    let text: String
    var attachments: [ComposerAttachment] = []
    // The names the daemon stored the attachments under, known once they are
    // uploaded; the timeline row names files by these.
    var uploaded: [UploadedAttachment]? = nil
    let sentAt: Date
    var status: String?
    var busyDelivery: String? = nil
    var turnSettings = TurnSettings()
    var failure: String? = nil

    static let clockSkew: TimeInterval = 5

    var files: [AttachedFile] {
        attachments.map { AttachedFile(name: $0.name, bytes: Int64($0.data.count)) }
    }

    static func label(for status: String?, evidence: String? = nil, waitingOnYou: Bool = false) -> String {
        if evidence == "transport_accepted" { return "Accepted · Not yet confirmed" }
        switch status {
        case "queued": return waitingOnYou ? "Queued · Waiting on you" : "Queued"
        case "submitted": return "Submitted"
        default: return "Sent"
        }
    }

    // The timeline shows a send's typed text with its files split out. Images
    // go to the runtime natively and appear as separate attachment rows, so an
    // image-only send records no user text at all; a runtime without native
    // images receives them as files instead.
    static func reconcile(_ sends: [LocalSend], with items: [TimelineItem]) -> [LocalSend] {
        var candidates = items.filter { ($0.kind == "message" || $0.kind == "attachment") && $0.role == "user" }
        return sends.filter { send in
            let text = send.text.trimmingCharacters(in: .whitespacesAndNewlines)
            guard let index = candidates.firstIndex(where: { item in
                guard send.matches(item, text: text) else { return false }
                guard let raw = item.timestamp, let at = SessionTree.parseTimestamp(raw) else { return true }
                return at >= send.sentAt.addingTimeInterval(-clockSkew)
            }) else { return true }
            candidates.remove(at: index)
            return false
        }
    }

    private func matches(_ item: TimelineItem, text: String) -> Bool {
        let names = uploaded.map { $0.map(\.name) } ?? attachments.map(\.name)
        let fileNames = uploaded.map { $0.filter { !ComposerAttachments.imageMediaTypes.contains($0.mediaType) }.map(\.name) } ?? attachments.filter { !$0.isImage }.map(\.name)
        if item.kind == "attachment" {
            return text.isEmpty && fileNames.isEmpty && attachments.contains(where: \.isImage)
        }
        guard PeerEnvelope(item.text).body.trimmingCharacters(in: .whitespacesAndNewlines) == text else { return false }
        let delivered = (item.files ?? []).map(\.name)
        guard !text.isEmpty || !delivered.isEmpty else { return false }
        return Set(fileNames).isSubset(of: delivered) && delivered.allSatisfy(names.contains)
    }
}
