#if DEBUG
import Foundation

enum PinPreview {
    nonisolated static var enabled: Bool { ProcessInfo.processInfo.arguments.contains("--preview-pins") }
    nonisolated(unsafe) private static var pinned: Set<String> = []
    nonisolated private static let lock = NSLock()

    nonisolated static func handles(_ action: [String: Any]) -> Bool {
        enabled && ["session-pin", "session-unpin"].contains(action["action"] as? String)
    }

    nonisolated static func respond(_ action: [String: Any]) -> String {
        guard let id = action["sessionId"] as? String else { return #"{"ok":true}"# }
        lock.withLock {
            switch action["action"] as? String {
            case "session-pin": pinned.insert(id)
            case "session-unpin": pinned.remove(id)
            default: break
            }
        }
        return #"{"ok":true,"sessionId":"\#(id)"}"#
    }

    nonisolated static func apply(_ json: String) -> String {
        let ids = lock.withLock { pinned }
        guard !ids.isEmpty, let root = try? JSONSerialization.jsonObject(with: Data(json.utf8)),
              let data = try? JSONSerialization.data(withJSONObject: mark(root, ids)) else { return json }
        return String(decoding: data, as: UTF8.self)
    }

    nonisolated private static func mark(_ value: Any, _ ids: Set<String>) -> Any {
        if var object = value as? [String: Any] {
            for (key, child) in object { object[key] = mark(child, ids) }
            if let id = object["id"] as? String, ids.contains(id), object["surface"] != nil { object["pinnedAt"] = "2026-09-07T12:00:00Z" }
            return object
        }
        if let array = value as? [Any] { return array.map { mark($0, ids) } }
        return value
    }
}
#endif
