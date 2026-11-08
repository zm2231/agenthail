import Foundation

/// A message that left the composer and has no answer from its computer yet.
struct PendingSend: Identifiable, Equatable {
    var alreadyUploaded: [String]? {
        let ids = attachments.compactMap(\.uploaded?.id)
        return !attachments.isEmpty && ids.count == attachments.count ? ids : nil
    }

    var unconfirmedTitle: String { retryStopped ? "Not confirmed" : "Not confirmed yet · retrying" }
    var offersRetry: Bool { retryStopped }

    var previewLine: String {
        let line = message.split(whereSeparator: \.isNewline).first.map(String.init)?.trimmingCharacters(in: .whitespaces) ?? ""
        return line.isEmpty ? "Message with attachments" : line
    }

    let id: UUID
    var sessionID: String
    let message: String
    let attachments: [ComposerAttachment]
    let idempotencyKey: String
    var uploaded: [String]?
    var files: [UploadedAttachment]?
    var wire: ActionWire?
    var unconfirmed = false
    let sentAt: Date
    let busyDelivery: String?
    var retryStopped = false
    var retryingOnTap = false

    init(id: UUID = UUID(), sessionID: String, message: String, attachments: [ComposerAttachment], idempotencyKey: String = UUID().uuidString, uploaded: [String]? = nil, wire: ActionWire? = nil, unconfirmed: Bool = false, sentAt: Date = Date(), busyDelivery: String? = nil) {
        self.sentAt = sentAt
        self.busyDelivery = busyDelivery
        let stored = attachments.compactMap(\.uploaded)
        files = !attachments.isEmpty && stored.count == attachments.count ? stored : nil
        self.id = id
        self.sessionID = sessionID
        self.message = message
        self.attachments = attachments
        self.idempotencyKey = idempotencyKey
        self.uploaded = uploaded
        self.wire = wire
        self.unconfirmed = unconfirmed
    }
}

enum SendOutcome: Equatable {
    case answered
    case refused(String)
    case unconfirmed

    // Any answer from the computer is definite. Without one, the message may have arrived.
    static func of(_ error: Error) -> SendOutcome {
        switch error {
        case let kept as KeptDelivery:
            return of(kept.error)
        case let error as AgenthailAPIError:
            switch error {
            case .invalidResponse, .outcomeUnknown, .unavailable: return .unconfirmed
            default: return .refused(AgenthailAPIError.userMessage(error))
            }
        case is URLError, is CancellationError: return .unconfirmed
        default: return .refused(AgenthailAPIError.userMessage(error))
        }
    }
}
