@testable import AgenthailHelpers
import Foundation

@main
struct SessionAttentionTest {
    static func main() {
        let caps = Capabilities(send: true, stream: true, reply: true, goal: false, compact: false, model: true, interrupt: true, steer: true)
        func session(_ id: String, attention: RowAttention? = nil) -> SessionState {
            var state = SessionState(id: id, surface: "codex", name: id, alias: nil, status: "idle", lastActive: "2026-10-04T11:00:00Z", queueCount: 0, open: true, current: true, currentReason: nil, capabilities: caps, readOnly: nil, readOnlyReason: nil, cwd: "/repo/main", project: ProjectIdentity(id: "agenthail", displayName: "agenthail", rootPath: "/repo", kind: "git"))
            state.attention = attention
            return state
        }

        let decoded = try! JSONDecoder().decode(SessionState.self, from: JSONEncoder().encode(session("asker")))
        expect(decoded.attention == nil, "a row without attention decodes as needing nobody")
        var json = String(decoding: try! JSONEncoder().encode(session("asker")), as: UTF8.self)
        json.removeLast()
        json += #","attention":{"kind":"ask","reason":"Ready to ship"}}"#
        let asking = try! JSONDecoder().decode(SessionState.self, from: Data(json.utf8))
        expect(asking.attention == RowAttention(kind: "ask", reason: "Ready to ship"), "the row's attention decodes as the daemon sent it")

        let row = try! JSONSerialization.jsonObject(with: JSONEncoder().encode(session("opened"))) as! [String: Any]
        let detailJSON: [String: Any] = ["session": row, "attention": ["kind": "request", "reason": "Answer in Agenthail"], "exchanges": [], "capabilities": row["capabilities"]!, "readOnly": false, "readOnlyReason": ""]
        let detail = try! JSONDecoder().decode(SessionDetail.self, from: JSONSerialization.data(withJSONObject: detailJSON))
        expect(detail.sessionState(open: true).attention == RowAttention(kind: "request", reason: "Answer in Agenthail"), "a row opened from its detail keeps the daemon's attention")

        let kinds: [(String, SessionAttention.Kind)] = [("request", .request), ("ask", .request), ("usageLimit", .usageLimit), ("stopped", .stopped), ("replacedRequest", .replacedRequest), ("later", .stopped)]
        for (kind, mapped) in kinds {
            let attention = SessionAttention(RowAttention(kind: kind, reason: "why"))
            expect(attention.kind == mapped && attention.reason == "why", "\(kind) maps to \(mapped) and keeps the daemon's reason")
        }

        let rows = [session("asker", attention: RowAttention(kind: "request", reason: "Answer in Agenthail")), session("idle"),
                    session("limited", attention: RowAttention(kind: "usageLimit", reason: "Usage limit reached")),
                    session("asker", attention: RowAttention(kind: "stopped", reason: "a later copy"))]
        let byID = SessionAttention.of(rows)
        expect(Set(byID.keys) == ["asker", "limited"], "only rows the daemon flags need you: \(byID.keys.sorted())")
        expect(byID["asker"] == SessionAttention(kind: .request, reason: "Answer in Agenthail"), "the first copy of a row wins")

        func item(_ id: String, _ reason: String, group: String = "waiting") -> String {
            #"{"group":"\#(group)","sessionId":"\#(id)","kind":"request","reason":"\#(reason)","answerable":false}"#
        }
        let page = try! JSONDecoder().decode(DecisionsPage.self, from: Data(#"{"decisions":[\#(item("stuck", "Usage limit reached", group: "stopped")),\#(item("asker", "Answer in Agenthail")),\#(item("asker", "a second item")),\#(item("busy", "Needs your approval"))],"leadHandling":[]}"#.utf8))
        var inbox = DecisionInbox()
        inbox.add(page, host: "", qualify: { $0 })
        let menu = MenuSessions(working: ["busy", "w1", "w2"], recent: ["asker", "r1", "idle", "stuck", "r2", "r3", "r4", "r5", "r6"])
        let shown = menu.shown(with: inbox)
        expect(shown.needsYou == ["stuck", "asker", "busy"], "Needs you lists the inbox in its order, a session once: \(shown.needsYou)")
        expect(shown.reasons == ["stuck": "Usage limit reached", "asker": "Answer in Agenthail", "busy": "Needs your approval"], "each reads as its first decision: \(shown.reasons)")
        expect(shown.working == ["w1", "w2"] && shown.recent == ["r1", "idle", "r2", "r3", "r4"], "Working and Recent leave out what Needs you lists, five each: \(shown.working) \(shown.recent)")
        expect(menu.shown(with: DecisionInbox()).needsYou.isEmpty && menu.shown(with: DecisionInbox()).working == ["busy", "w1", "w2"], "an empty inbox leaves the rest as they are")
        expect(menu.shown(with: inbox, limit: 2).needsYou == ["stuck", "asker"], "Needs you stops at the limit")
        print("session attention tests passed")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
