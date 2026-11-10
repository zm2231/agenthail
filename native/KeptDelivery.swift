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

    let id: String
    var choices: [String] = []

    var offersDelete: Bool { choices.contains(Self.delete) }

    func reading(_ requests: SessionRequests) -> FailedSendAsk {
        let options = requests.requests.first { $0.id == id }?.questions?.first?.options ?? []
        return FailedSendAsk(id: id, choices: options.map(\.id))
    }
}
