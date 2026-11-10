#if DEBUG
import Foundation

enum AskPreview {
    nonisolated static var enabled: Bool { ProcessInfo.processInfo.arguments.contains("--preview-ask") }
    nonisolated(unsafe) static var answered = false

    static let requestID = "ask-447a922cae82"

    nonisolated static var requestsJSON: String {
        answered ? #"{"permissionMode":"","requests":[]}"# : #"{"permissionMode":"","requests":[{"id":"\#(requestID)","kind":"ask","tool":"","title":"Ready to ship","prompt":"Ready to ship","questions":[{"question":"Deploy it now?","header":"","multiSelect":false,"options":[{"id":"o0","label":"Deploy","description":"Ship the build that passed review"},{"id":"o1","label":"Wait","description":"hold until Monday"}],"suggested":0,"links":[{"title":"Runbook","url":"https://example.com/runbook"}]}],"actions":[{"id":"o0","title":"Deploy"},{"id":"o1","title":"Wait"},{"id":"reply","title":"Reply…"}],"alwaysAllow":false,"createdAt":"2026-09-12T04:03:00Z"}]}"#
    }

    nonisolated static func apply(_ snapshot: inout [String: Any]) {
        var sessions = snapshot["sessions"] as! [[String: Any]]
        var ask: [String: Any] = ["id": requestID, "title": "Ready to ship", "routedTo": "person", "askedAt": "2026-09-12T04:03:00Z"]
        if answered { ask["answeredAt"] = "2026-09-12T04:05:00Z" } else { sessions[0]["attention"] = ["kind": "ask", "reason": "Ready to ship"] }
        sessions[0]["ask"] = ask
        sessions[0]["endTurn"] = ["headline": "Ready to ship", "endedAt": "2026-09-12T04:03:00Z"]
        sessions[0]["status"] = "idle"
        sessions[0]["alias"] = "builder"
        snapshot["sessions"] = sessions
    }

    // Accepts only the contract's payload, so a wrong body surfaces as an error on screen.
    nonisolated static func answer(_ action: [String: Any]) -> (status: Int, body: String) {
        let decision = action["decision"] as? [String: Any] ?? [:]
        guard action["action"] as? String == "respond", action["requestId"] as? String == requestID,
              decision["choices"] as? [String] == ["o1"], decision["answers"] == nil,
              decision["message"] as? String == "After lunch." else {
            let sent = (try? JSONSerialization.data(withJSONObject: action, options: .sortedKeys)).flatMap { String(data: $0, encoding: .utf8) } ?? ""
            return (400, #"{"ok":false,"error":{"code":"invalid_request","message":"unexpected answer \#(sent.replacingOccurrences(of: "\"", with: "'"))"}}"#)
        }
        if answered { return (409, #"{"ok":false,"error":{"code":"stale","message":"already answered"}}"#) }
        answered = true
        return (200, #"{"ok":true,"result":{"requestId":"\#(requestID)","outcome":"answered","receipt":{}}}"#)
    }
}
#endif
