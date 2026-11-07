import Foundation

struct RequestAnswerFailure: Equatable {
    static let staleMessage = "That request was already answered or has expired."
    static let maybeDeliveredMessage = "Your answer may have reached the session, so Agenthail did not send it again. Check the session before writing more."

    let message: String
    let dropsCard: Bool

    init(_ error: Error) {
        switch error {
        case AgenthailAPIError.requestStale:
            (message, dropsCard) = (Self.staleMessage, true)
        case AgenthailAPIError.request(502, let detail) where Self.mayHaveArrived(detail):
            (message, dropsCard) = (Self.maybeDeliveredMessage, true)
        default:
            (message, dropsCard) = (error.localizedDescription, false)
        }
    }

    // The daemon's wording is the contract: such an answer stays sent and must not be retried.
    static func mayHaveArrived(_ detail: String) -> Bool {
        detail.localizedCaseInsensitiveContains("may have reached the session")
    }
}
