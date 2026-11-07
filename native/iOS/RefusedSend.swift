import Foundation

struct RefusedSend: Equatable {
    let message: String
    let attachments: [ComposerAttachment]
    var deliveryID: Int64? = nil
    var deletable = false

    var canDelete: Bool { deliveryID == nil || deletable }

    func removed(from text: String, attachments current: [ComposerAttachment]) -> (text: String, attachments: [ComposerAttachment]) {
        var remaining = current
        for attachment in attachments {
            if let index = remaining.firstIndex(where: { $0.name == attachment.name && $0.data == attachment.data && $0.uploaded == attachment.uploaded }) {
                remaining.remove(at: index)
            }
        }
        if text.trimmingCharacters(in: .whitespacesAndNewlines) == message { return ("", remaining) }
        if text.hasPrefix(message + "\n\n") { return (String(text.dropFirst(message.count + 2)), remaining) }
        return (text, remaining)
    }
}
