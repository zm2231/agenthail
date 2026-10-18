import Foundation

@main
struct QueueFactsTest {
    static func main() {
        let caps = Capabilities()
        func queue(_ id: Int64, status: String, evidence: String = "queued", lastError: String? = nil, session: String = "s1") -> QueueState {
            var fields: [String: Any] = ["id": id, "sessionId": session, "target": "@builder", "message": "check the build", "status": status, "attempts": 1, "queuedAt": "2026-10-18 06:00:00", "evidence": evidence]
            if let lastError { fields["lastError"] = lastError }
            return try! JSONDecoder().decode(QueueState.self, from: JSONSerialization.data(withJSONObject: fields))
        }
        func session(_ id: String, surface: String = "claude", status: String = "idle", readOnly: Bool? = nil, reason: String? = nil, transport: String? = nil) -> SessionState {
            var value = SessionState(id: id, surface: surface, name: id, alias: nil, status: status, lastActive: nil, queueCount: 0, open: true, current: false, currentReason: nil, capabilities: caps, readOnly: readOnly, readOnlyReason: reason)
            value.transport = transport
            return value
        }
        let attention = try! JSONDecoder().decode([AttentionState].self, from: Data(#"[{"id":1,"sessionId":"s1","target":"@builder","queueId":7,"reason":"Claude needs to reconnect","requestedAction":"Reconnect Claude, then retry or cancel this message","createdAt":"2026-10-18 06:01:00"}]"#.utf8))

        let pending = queue(1, status: "pending")
        let dead = queue(7, status: "dead", evidence: "failed", lastError: "delivery rejected [authentication needed]: 401")
        let expired = queue(9, status: "expired", evidence: "expired")

        let writable = session("s1")
        expect(QueueFacts.canCancel(pending) && !QueueFacts.canRetry(pending, target: writable), "pending offers Cancel only")
        expect(QueueFacts.canRetry(dead, target: writable) && QueueFacts.canCancel(dead), "dead offers Retry and Cancel when the target is writable")
        expect(!QueueFacts.canRetry(dead, target: session("s1", readOnly: true, reason: "Closed in Claude")) && QueueFacts.canCancel(dead), "a read-only target offers Cancel but not Retry, since the API rejects the retry")
        expect(!QueueFacts.canRetry(dead, target: nil), "an unknown target offers no Retry")
        expect(!QueueFacts.canRetry(expired, target: writable) && !QueueFacts.canCancel(expired), "expired offers nothing")

        expect(QueueFacts.reason(pending, target: session("s1", status: "busy")) == "Waiting for this agent to finish", "busy target waits")
        expect(QueueFacts.reason(pending, target: session("s1")) == "Waiting for delivery", "idle target waits for delivery")
        expect(QueueFacts.reason(pending, target: nil) == "Waiting for delivery", "unknown target waits for delivery")
        expect(QueueFacts.reason(pending, target: session("s1", readOnly: true, reason: "Closed in Claude")) == "Closed in Claude", "read-only target gives its reason")
        expect(QueueFacts.reason(pending, target: session("s1", readOnly: true)) == "Target is not writable", "read-only target without a reason")
        expect(QueueFacts.reason(pending, target: session("s1", surface: "codex", status: "notLoaded", transport: "desktop")) == "Loading this Codex conversation before delivery", "desktop Codex loads first")
        expect(QueueFacts.reason(pending, target: session("s1", surface: "codex", status: "notLoaded")) == "Codex conversation is not available to load", "unloadable Codex")
        expect(QueueFacts.reason(queue(2, status: "pending", evidence: "transport_accepted"), target: nil) == "Transport accepted; receiver completion is pending", "transport accepted evidence wins")
        expect(QueueFacts.reason(queue(3, status: "dead", evidence: "unknown"), target: nil) == "Delivery outcome is unknown", "unknown evidence")
        expect(QueueFacts.reason(dead, target: nil) == "Delivery failed", "failed evidence")
        expect(QueueFacts.reason(expired, target: nil) == "Expired", "non-pending status uses its label")

        expect(QueueFacts.statusLabel("dead") == "Needs a decision" && QueueFacts.statusLabel("pending") == "Waiting", "status labels are by real status")
        expect(QueueFacts.lastError(queue(4, status: "dead", lastError: "  ")) == nil, "blank last error is hidden")

        expect(QueueFacts.detail(dead, target: nil, attention: attention) == ["Claude needs to reconnect", "Reconnect Claude, then retry or cancel this message", "delivery rejected [authentication needed]: 401"], "a dead item with attention shows reason, requested action and last error")
        expect(QueueFacts.detail(pending, target: session("s1", status: "busy"), attention: attention) == ["Waiting for this agent to finish"], "an item without attention shows its wait reason")
        expect(QueueFacts.attention(attention, for: "s1").count == 1 && QueueFacts.attention(attention, for: "s2").isEmpty, "attention filters by session")
        expect(QueueFacts.undelivered([pending, dead, queue(5, status: "delivered"), queue(6, status: "pending", session: "s2")], for: "s1").map(\.id) == [1, 7], "undelivered keeps this session's non-delivered items")

        let decoded = try! JSONDecoder().decode(SessionState.self, from: Data(#"{"id":"s1","surface":"codex","name":"n","status":"notLoaded","queueCount":0,"open":false,"current":false,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":true,"interrupt":true,"steer":true},"transport":"desktop"}"#.utf8))
        expect(decoded.transport == "desktop", "session transport decodes from the snapshot")
        print("queue facts tests passed")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
