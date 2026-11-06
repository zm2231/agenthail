#if DEBUG
import Foundation

extension SessionPreview {
    static var servesOlderPage: Bool { ProcessInfo.processInfo.arguments.contains("--preview-older") }
    static var delaysOlderPage: Bool { ProcessInfo.processInfo.arguments.contains("--preview-older-slow") }

    nonisolated static func messages(_ prefix: String, _ label: String, hour: Int) -> [[String: Any]] {
        (1...40).map { n in
            ["id": "\(prefix)-\(n)", "kind": "message", "role": n.isMultiple(of: 2) ? "assistant" : "user", "title": n.isMultiple(of: 2) ? "assistant" : "user",
             "text": "\(label) \(n). " + String(repeating: "It covers what was decided at this point of the session. ", count: n == 1 ? 12 : 1 + n % 4),
             "timestamp": "2026-09-07T\(hour):\(String(format: "%02d", n)):00Z", "truncated": false]
        }
    }

    nonisolated static func olderPageDetail(base: String, older: Bool) -> String {
        var detail = try! JSONSerialization.jsonObject(with: Data(base.utf8)) as! [String: Any]
        var timeline = detail["timeline"] as! [String: Any]
        if older {
            timeline["items"] = messages("older", "Earlier message", hour: 10)
            timeline["nextBefore"] = 0
        } else {
            timeline["items"] = messages("recent", "Recent message", hour: 11) + (timeline["items"] as! [[String: Any]])
            timeline["nextBefore"] = 2400
        }
        detail["timeline"] = timeline
        return String(data: try! JSONSerialization.data(withJSONObject: detail), encoding: .utf8)!
    }
}
#endif
