#if DEBUG
import Foundation

extension SessionPreview {
    static var showsLinks: Bool { ProcessInfo.processInfo.arguments.contains("--preview-links") }

    nonisolated static func linksDetail(base: String) -> String {
        var detail = try! JSONSerialization.jsonObject(with: Data(base.utf8)) as! [String: Any]
        var timeline = detail["timeline"] as! [String: Any]
        let items: [[String: Any]] = [
            ["id": "links-user", "kind": "message", "role": "user", "title": "user", "text": "Is the status page at https://example.com/status up yet?", "timestamp": "2026-09-07T12:10:00Z", "truncated": false],
            ["id": "links-agent", "kind": "message", "role": "user", "title": "user", "text": "Staging is up at `https://example.com/staging`", "timestamp": "2026-09-07T12:10:30Z", "truncated": false,
             "agent": ["direction": "in", "kind": "peer", "peer": "@helper", "summary": "Staging is up"]],
            ["id": "links-reply", "kind": "message", "role": "assistant", "title": "assistant", "text": "The dashboard is running at `http://10.0.0.8:8317/management.html` now, and the plain page is http://10.0.0.8:8317/status.html.\n\nThe docs live at https://example.com/docs.", "timestamp": "2026-09-07T12:11:00Z", "truncated": false],
        ]
        timeline["items"] = (timeline["items"] as! [[String: Any]]) + items
        detail["timeline"] = timeline
        return String(data: try! JSONSerialization.data(withJSONObject: detail), encoding: .utf8)!
    }
}
#endif
