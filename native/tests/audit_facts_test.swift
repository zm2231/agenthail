@testable import AgenthailHelpers
import Foundation

@main
struct AuditFactsTest {
    static func main() {
        func event(_ json: String) -> HistoryState {
            try! JSONDecoder().decode(HistoryState.self, from: Data(json.utf8))
        }
        let routed = event(#"{"id":1,"createdAt":"2026-10-18 06:00:00","kind":"peer_send","sessionId":"s2","source":"claude/builder","target":"claude/reviewer","message":"Synthetic: please review\n\nthe second paragraph is kept in full.","result":"queued"}"#)
        let failed = event(#"{"id":2,"createdAt":"2026-10-18 06:01:00","kind":"deliver","sessionId":"s2","target":"claude/reviewer","message":"Synthetic: hello","error":"delivery rejected: synthetic 401"}"#)
        let bare = event(#"{"id":3,"createdAt":"2026-10-18 06:02:00","kind":"settings_update","sessionId":"","target":"","source":"","message":"","result":"","error":""}"#)
        let sessionOnly = event(#"{"id":4,"createdAt":"2026-10-18 06:03:00","kind":"relay_fired","sessionId":"s9","result":"relay fired"}"#)

        expect(AuditFacts.source(routed) == "claude/builder" && AuditFacts.target(routed) == "claude/reviewer", "route shows source and target")
        expect(AuditFacts.source(failed) == nil, "no source means target only")
        expect(AuditFacts.target(sessionOnly) == "s9", "target falls back to the session id")
        expect(AuditFacts.target(bare) == "Agenthail" && AuditFacts.source(bare) == nil, "empty strings fall back like the web")

        expect(AuditFacts.content(routed) == "Synthetic: please review\n\nthe second paragraph is kept in full.", "message content is kept in full")
        expect(AuditFacts.content(failed) == "delivery rejected: synthetic 401" && AuditFacts.isError(failed), "error wins over message")
        expect(AuditFacts.content(sessionOnly) == "relay fired" && !AuditFacts.isError(sessionOnly), "result when there is no message")
        expect(AuditFacts.content(bare) == "No message content recorded" && !AuditFacts.isError(bare), "empty content says so")

        let peer = "<cross-session-message from=\"uds:/x.sock\" from-session=\"synthetic-session\" from-name=\"builder\" from-mode=\"auto\">Synthetic: ping</cross-session-message>"
        let wrapped = HistoryState(id: 5, createdAt: "", kind: "peer_received", sessionId: nil, sourceSessionId: nil, target: nil, source: nil, queueId: nil, message: peer, result: nil, error: nil)
        expect(AuditFacts.content(wrapped) == peer, "peer envelopes keep every recorded attribute, as the web shows them")

        expect(AuditFacts.kindLabel("peer_send") == "Peer send" && AuditFacts.kindLabel("") == "Event", "kind labels match the web")
        expect(AuditFacts.kindLabel("transport-accepted") == "Transport accepted", "hyphenated kinds read as words")
        let selfRoute = event(#"{"id":6,"createdAt":"","kind":"peer_send","sessionId":"s3","source":"claude/builder","target":"claude/builder","message":"m"}"#)
        expect(AuditFacts.source(selfRoute) == nil && AuditFacts.target(selfRoute) == "claude/builder", "a route to itself names the session once")
        let compacted = event(#"{"id":7,"createdAt":"","kind":"delivered","queueId":3,"operation":"compact"}"#)
        let message = event(#"{"id":8,"createdAt":"","kind":"delivered","queueId":4,"operation":"message"}"#)
        expect(AuditFacts.heading(compacted) == "Delivered · Compact", "a queued operation is named on its history row")
        expect(AuditFacts.heading(message) == "Delivered" && AuditFacts.heading(routed) == "Peer send", "a message or an older row reads as its kind")
        print("audit facts tests passed")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
