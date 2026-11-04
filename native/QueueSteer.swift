import Foundation

enum QueueSteer {
    static func waiting(_ items: [QueueState]) -> [QueueState] {
        items.filter { $0.status == "pending" && ["", "message"].contains($0.operation ?? "") }.sorted { $0.id < $1.id }
    }

    static func refusal(_ item: QueueState, capabilities: Capabilities) -> String? {
        if let author = item.author, author != "person" {
            return "This message came from another session, and Steer would send it as yours. It sends when the turn ends."
        }
        if carriesTurnOptions(item) {
            return "Steer can't carry this message's model or turn settings. It sends when the turn ends."
        }
        let attachments = item.attachments ?? []
        guard !attachments.isEmpty else { return nil }
        if attachments.contains(where: { $0.missing == true }) {
            return "An attached file is gone. Use Edit to send this message without it."
        }
        guard let steerImages = capabilities.steerImages else {
            return "Update Agenthail on this computer to steer attachments. This message sends when the turn ends."
        }
        if !steerImages && attachments.contains(where: isImage) {
            return "Attached images can't join a running turn on this session. This message sends when the turn ends."
        }
        return nil
    }

    private static func carriesTurnOptions(_ item: QueueState) -> Bool {
        [item.model, item.effort, item.mode, item.serviceTier].contains { !($0 ?? "").isEmpty } || item.outputSchema != nil
    }

    private static func isImage(_ attachment: QueueAttachment) -> Bool {
        ["image/png", "image/jpeg", "image/gif"].contains(attachment.mediaType)
    }
}

extension AgenthailAPI {
    // The daemon refuses attachments on the steer action, so they steer as a send.
    func steerQueued(message: String, attachments: [QueueAttachment], sessionID: String) async throws -> ActionReceipt {
        guard !attachments.isEmpty else {
            return try await sendInstruction(action: "steer", sessionID: sessionID, message: message, author: "person")
        }
        return try await sendInstruction(action: "send", sessionID: sessionID, message: message, busyDelivery: "steer", author: "person", attachments: attachments.map(\.id))
    }
}
