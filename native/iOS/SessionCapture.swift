#if DEBUG
import Foundation

struct SessionCapture: Sendable {
    let snapshotJSON: String
    let detailJSON: [String: String]
    let queueJSON: String
    let initialSessionID: String

    static let current: SessionCapture? = {
        guard ProcessInfo.processInfo.arguments.contains("--preview-capture") else { return nil }
        do {
            let url = URL.documentsDirectory.appending(path: "AgenthailCapture.json")
            let value = try JSONSerialization.jsonObject(with: Data(contentsOf: url)) as! [String: Any]
            func json(_ value: Any) throws -> String { String(data: try JSONSerialization.data(withJSONObject: value), encoding: .utf8)! }
            return try SessionCapture(snapshotJSON: json(value["snapshot"]!), detailJSON: (value["details"] as! [String: Any]).mapValues(json), queueJSON: json(value["queue"]!), initialSessionID: value["initialSessionID"] as! String)
        } catch { fatalError("The requested local session capture could not be read: \(error)") }
    }()
}
#endif
