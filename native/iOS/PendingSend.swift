import Foundation

/// A message that left the composer and has no answer from its computer yet.
struct PendingSend: Identifiable, Equatable {
    var alreadyUploaded: [String]? {
        let ids = attachments.compactMap(\.uploaded?.id)
        return !attachments.isEmpty && ids.count == attachments.count ? ids : nil
    }

    let id: UUID
    var sessionID: String
    let message: String
    let attachments: [ComposerAttachment]
    let turnSettings: TurnSettings
    let idempotencyKey: String
    var uploaded: [String]?
    var wire: ActionWire?
    var unconfirmed = false

    init(id: UUID = UUID(), sessionID: String, message: String, attachments: [ComposerAttachment], turnSettings: TurnSettings, idempotencyKey: String = UUID().uuidString, uploaded: [String]? = nil, wire: ActionWire? = nil, unconfirmed: Bool = false) {
        self.id = id
        self.sessionID = sessionID
        self.message = message
        self.attachments = attachments
        self.turnSettings = turnSettings
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
