import Foundation

struct KeptDelivery: LocalizedError {
    let error: Error
    let deliveryID: Int64
    let deletable: Bool

    var errorDescription: String? { AgenthailAPIError.userMessage(error) }

    static func wrapping(_ error: Error, body: Data) -> Error {
        guard let object = try? JSONSerialization.jsonObject(with: body) as? [String: Any],
              let id = (object["deliveryId"] as? NSNumber)?.int64Value, id > 0 else { return error }
        return KeptDelivery(error: error, deliveryID: id, deletable: object["deletable"] as? Bool ?? false)
    }
}
