import Foundation

struct KeptDelivery: LocalizedError {
    let error: Error
    let askID: String

    var errorDescription: String? { AgenthailAPIError.userMessage(error) }

    static func wrapping(_ error: Error, body: Data) -> Error {
        guard let object = try? JSONSerialization.jsonObject(with: body) as? [String: Any],
              let id = object["askId"] as? String, !id.isEmpty else { return error }
        return KeptDelivery(error: error, askID: id)
    }
}

struct FailedSendAsk: Equatable {
    static let delete = "delete"
    static let retry = "retry"

    let id: String
    var choices: [String] = []

    var offersDelete: Bool { choices.contains(Self.delete) }

    func reading(_ requests: SessionRequests) -> FailedSendAsk {
        let options = requests.requests.first { $0.id == id }?.questions?.first?.options ?? []
        return FailedSendAsk(id: id, choices: options.map(\.id))
    }
}

// How a failed send's ask was settled when Delete on it finds it no longer open.
enum SettledAsk: Equatable {
    // Deleted, dismissed or withdrawn: the message never reached the session.
    case gone
    // Retried from another control: the daemon is sending it again.
    case resent

    static let resentNotice = "That message was already sent again from another device, so it was not deleted."

    static func of(_ error: Error) -> SettledAsk? {
        guard case AgenthailAPIError.requestStale(_, let settled?) = error else { return nil }
        return settled == FailedSendAsk.retry ? .resent : .gone
    }
}
