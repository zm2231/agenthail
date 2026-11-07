#if DEBUG
import Foundation

extension SessionPreview {
    nonisolated static var longHistory: [[String: Any]] {
        (1...150).map { n in
            ["id": "history-\(n)", "kind": "message", "role": n.isMultiple(of: 2) ? "assistant" : "user", "title": n.isMultiple(of: 2) ? "assistant" : "user",
             "text": "Earlier step \(n). " + String(repeating: "The build was checked again after the reconnect change and the queue kept its order. ", count: 1 + (n * 7) % 11),
             "timestamp": "2026-09-12T02:\(String(format: "%02d", n % 60)):00Z", "truncated": false]
        }
    }
}
#endif
