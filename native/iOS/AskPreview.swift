#if DEBUG
import Foundation

enum AskPreview {
    nonisolated static var enabled: Bool { ProcessInfo.processInfo.arguments.contains("--preview-ask") }
    nonisolated(unsafe) static var answered = false

    static let requestID = "ask-447a922cae82"

    nonisolated static var requestsJSON: String {
        answered ? #"{"permissionMode":"","requests":[]}"# : #"{"permissionMode":"","requests":[{"id":"\#(requestID)","kind":"ask","tool":"","title":"Ready to ship","prompt":"Ready to ship","questions":[{"question":"Deploy it now?","header":"","multiSelect":false,"options":[{"id":"o0","label":"Deploy","description":"Ship the build that passed review"},{"id":"o1","label":"Wait","description":"hold until Monday"}],"suggested":0,"links":[{"title":"Runbook","url":"https://example.com/runbook"}]}],"actions":[{"id":"o0","title":"Deploy"},{"id":"o1","title":"Wait"},{"id":"reply","title":"Reply…"}],"alwaysAllow":false,"createdAt":"2026-09-12T04:03:00Z"}\#(second)]}"#
    }

    nonisolated private static var second: String {
        guard ProcessInfo.processInfo.arguments.contains("--preview-two-asks") else { return "" }
        return #",{"id":"ask-b684039d968c","kind":"ask","tool":"","title":"Sign in to the store","prompt":"Sign in to the store","questions":[{"question":"Open the sign-in link?","header":"","multiSelect":false,"options":[{"id":"o0","label":"Opened it","description":""}]}],"actions":[{"id":"o0","title":"Opened it"}],"alwaysAllow":false,"createdAt":"2026-09-12T05:25:00Z"}"#
    }

    nonisolated static var decisionsJSON: String {
        let requests = (try? JSONSerialization.jsonObject(with: Data(requestsJSON.utf8)) as? [String: Any])?["requests"] as? [Any] ?? []
        let decisions: [[String: Any]] = answered ? [] : [["group": "waiting", "sessionId": "demo", "kind": "ask", "reason": "Ready to ship", "since": "2026-09-12T04:03:00Z", "answerable": true, "requests": requests]]
        return String(data: try! JSONSerialization.data(withJSONObject: ["decisions": decisions, "leadHandling": []]), encoding: .utf8)!
    }

    nonisolated static func apply(_ snapshot: inout [String: Any]) {
        var sessions = snapshot["sessions"] as! [[String: Any]]
        var ask: [String: Any] = ["id": requestID, "title": "Ready to ship", "routedTo": "person", "askedAt": "2026-09-12T04:03:00Z"]
        if !second.isEmpty { ask["open"] = [requestID, "ask-b684039d968c"] }
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
        let noted = decision["choices"] as? [String] == ["o1"] && decision["answers"] == nil && decision["message"] as? String == "After lunch."
        let recommended = decision["choices"] as? [String] == ["o0"] && decision.count == 1
        guard action["action"] as? String == "respond", action["requestId"] as? String == requestID, noted || recommended else {
            let sent = (try? JSONSerialization.data(withJSONObject: action, options: .sortedKeys)).flatMap { String(data: $0, encoding: .utf8) } ?? ""
            return (400, #"{"ok":false,"error":{"code":"invalid_request","message":"unexpected answer \#(sent.replacingOccurrences(of: "\"", with: "'"))"}}"#)
        }
        if answered { return (409, #"{"ok":false,"error":{"code":"stale","message":"already answered"}}"#) }
        answered = true
        return (200, #"{"ok":true,"result":{"requestId":"\#(requestID)","outcome":"answered","receipt":{}}}"#)
    }
}
enum InboxCardsPreview {
    nonisolated static var enabled: Bool { ProcessInfo.processInfo.arguments.contains("--preview-inbox-cards") }
    nonisolated(unsafe) static var sent: [String] = []
    nonisolated(unsafe) static var dismissed: Set<String> = []

    nonisolated static func apply(_ snapshot: inout [String: Any]) {
        var sessions = snapshot["sessions"] as! [[String: Any]]
        let aliases = ["demo": "search-indexing-and-ranking-overhaul-worker", "codex-demo": "release-pipeline-lead"]
        for index in sessions.indices {
            if let alias = aliases[sessions[index]["id"] as? String ?? ""] { sessions[index]["alias"] = alias }
        }
        snapshot["sessions"] = sessions
        snapshot["decisionsRev"] = "preview-1"
    }

    nonisolated static let question = "Should search rank by title matches only, or weigh matches in the body and comments too?"

    nonisolated private static func parked(_ session: String, _ item: String, _ title: String?, _ since: String, reason: String = question) -> [String: Any] {
        var row: [String: Any] = ["group": "waiting", "sessionId": session, "projectId": "project-fieldnotes", "kind": "parked", "workItem": item, "reason": reason, "since": since, "answerable": false]
        if let title { row["workTitle"] = title }
        return row
    }

    nonisolated static func decisionsJSON(host: String?) -> String {
        let requests = (try? JSONSerialization.jsonObject(with: Data(AskPreview.requestsJSON.utf8)) as? [String: Any])?["requests"] as? [Any] ?? []
        let decisions: [[String: Any]] = host == nil ? [
            ["group": "holding", "sessionId": "codex-demo", "projectId": "project-fieldnotes", "kind": "ask", "reason": "Ready to ship", "since": "2026-09-12T04:03:00Z", "answerable": true, "requests": requests, "holdsUp": ["demo"]],
            parked("demo", "fieldnotes-W8", "Rank search results by title matches before body matches", "2026-09-12T03:00:00Z"),
            parked("demo", "fieldnotes-W9", "Show matching comment snippets under each search result", "2026-09-12T03:05:00Z"),
            parked("demo", "fieldnotes-W10", "Weigh comment matches lower than body matches", "2026-09-12T03:10:00Z"),
            parked("demo", "fieldnotes-W12", "Pick the default date range for the usage chart", "2026-09-12T03:20:00Z", reason: "Default the usage chart to the last 7 days or the last 30?"),
        ] : [
            parked("computer::demo", "fieldnotes-W8", nil, "2026-09-12T03:00:00Z", reason: "fieldnotes-W8 waits on you: " + question),
            parked("computer::demo", "fieldnotes-W10", nil, "2026-09-12T03:10:00Z", reason: "fieldnotes-W10 waits on you: " + question),
        ]
        let shown = decisions.filter { !dismissed.contains($0["workItem"] as? String ?? $0["sessionId"] as? String ?? "") }
        return String(data: try! JSONSerialization.data(withJSONObject: ["decisions": shown, "leadHandling": []]), encoding: .utf8)!
    }

    nonisolated static func send(_ action: [String: Any]) -> (status: Int, body: String) {
        if action["action"] as? String == "dismiss-decision" {
            if let item = action["workItem"] as? String { dismissed.insert(item) } else if let session = action["sessionId"] as? String { dismissed.insert(session) }
            return (200, #"{"ok":true,"result":{"outcome":"dismissed"}}"#)
        }
        guard action["action"] as? String == "send", let message = action["message"] as? String else {
            return (400, #"{"ok":false,"error":{"code":"invalid_request","message":"not a send"}}"#)
        }
        sent.append(message)
        return (200, #"{"ok":true,"result":{"evidence":"submitted"}}"#)
    }
}
#endif
