#if DEBUG
import Foundation

enum TilesPreview {
    static var enabled: Bool { ProcessInfo.processInfo.arguments.contains("--preview-tiles") }

    static let names = ["billing", "api", "site", "web", "docs", "mobile"]

    static var projects: [[String: Any]] {
        names.enumerated().map { index, name in
            [
                "id": "p-" + name,
                "displayName": name,
                "checkouts": [],
                "coordinators": [["name": name + "-lead", "surface": "claude"]],
                "activity": ["id": "p-" + name, "lastActive": String(format: "2026-09-12T0%d:00:00Z", 9 - index), "sessionCount": 1, "checkouts": []],
            ]
        }
    }

    nonisolated static let decisionsJSON = #"{"decisions":[{"group":"waiting","sessionId":"demo","projectId":"p-api","kind":"approval","reason":"Needs your approval","since":"2026-09-12T04:00:00Z","answerable":false},{"group":"stopped","sessionId":"codex-demo","projectId":"p-api","kind":"usageLimit","reason":"Usage limit until 3:00","since":"2026-09-12T03:00:00Z","answerable":false}],"leadHandling":[]}"#
}
#endif
