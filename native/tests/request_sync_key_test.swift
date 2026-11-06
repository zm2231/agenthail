@testable import AgenthailHelpers
import Foundation

@main
struct RequestSyncKeyTest {
    static func main() {
        let writable = session(readOnly: false)
        let readOnly = session(readOnly: true)
        expect(RequestSync.viewKey(writable) != RequestSync.viewKey(readOnly), "a session turning read-only resyncs its requests, which clears a request error")
        expect(RequestSync.viewKey(writable) == RequestSync.viewKey(session(readOnly: false)), "an unchanged session keeps its key")

        let waiting = session(respond: false, endTurn: #"{"headline":"Ready to ship","needs":"you","endedAt":"2026-11-06T04:52:30Z","requestId":"turn-end-1"}"#)
        expect(waiting.endTurn?.headline == "Ready to ship" && waiting.pendingAskID == "turn-end-1", "the row's waiting ask decodes: \(String(describing: waiting.endTurn))")
        expect(RequestSync.lists(waiting), "an ask is listed though the provider answers nothing through Agenthail")
        expect(!RequestSync.lists(session(respond: false)), "a session that answers nothing and has no ask lists nothing")
        expect(!RequestSync.lists(session(readOnly: true, respond: false, endTurn: #"{"needs":"you","requestId":"turn-end-1"}"#)), "a read-only session's ask is not listed")
        let newer = session(respond: false, endTurn: #"{"needs":"you","requestId":"turn-end-2"}"#)
        expect(RequestSync.cacheKey(waiting) != RequestSync.cacheKey(newer), "a new ask reads again")
        expect(RequestSync.cacheKey(waiting).hasPrefix("s1|"), "the key starts with the session, which keeps cards shown while it rereads")

        let answered = session(respond: false, endTurn: #"{"needs":"you","requestId":"turn-end-1","answeredAt":"2026-11-06T04:55:02Z"}"#)
        expect(answered.pendingAskID == nil && !RequestSync.lists(answered), "an answered ask is not fetched")
        expect(session(endTurn: #"{"needs":"nothing","endedAt":"2026-11-06T04:52:30Z"}"#).pendingAskID == nil, "a turn that needs nothing has no ask")
        expect(session(endTurn: #"{"needs":"you","askedSession":"lead"}"#).pendingAskID == nil, "a needs-you told to an agent has no ask")

        let ask = SessionRequest(id: "turn-end-1", kind: "ask", tool: "")
        let approval = SessionRequest(id: "a1", kind: "approval", tool: "Bash")
        let listed = SessionRequests(permissionMode: "", requests: [approval, ask])
        expect(listed.offered(pendingAsk: "turn-end-1").map(\.id) == ["a1", "turn-end-1"], "the waiting ask follows provider requests")
        expect(listed.offered(pendingAsk: nil).map(\.id) == ["a1"], "an answered ask is never offered from a stale read")
        expect(listed.offered(pendingAsk: "turn-end-2").map(\.id) == ["a1"], "a retired ask is never offered")
        expect(listed.offered(pendingAsk: "turn-end-1", retired: "turn-end-1").map(\.id) == ["a1"], "an ask answered here is not offered while the row catches up")

        var retired = RetiredAsks()
        retired.retire("turn-end-1", in: "s1")
        expect(!retired.settles(waiting) && retired.retired(in: "s1") == "turn-end-1", "the entry holds while the row still names the ask")
        expect(retired.retired(in: "other") == nil, "an entry belongs to its session")
        var caughtUp = retired
        caughtUp.settle(answered)
        expect(caughtUp.retired(in: "s1") == nil, "the entry goes once the row shows answeredAt")
        var moved = retired
        moved.settle(newer)
        expect(moved.retired(in: "s1") == nil && listed.offered(pendingAsk: "turn-end-1", retired: moved.retired(in: "s1")).count == 2, "a new ask drops the entry, so it cannot hide anything")
        let sent = RequestAnswerFailure(AgenthailAPIError.request(502, "session is gone"))
        expect(RequestAnswerFailure.retires(ask, failure: nil) && !RequestAnswerFailure.retires(ask, failure: sent) && !RequestAnswerFailure.retires(approval, failure: nil), "only a sent or terminally failed ask retires")
        print("request sync key tests passed")
    }

    static func session(readOnly: Bool = false, respond: Bool = true, endTurn: String? = nil) -> SessionState {
        let end = endTurn.map { #","endTurn":"# + $0 } ?? ""
        let json = #"{"id":"s1","surface":"claude","name":"Lead","status":"idle","queueCount":0,"open":true,"current":true,"readOnly":"# + "\(readOnly)" + #","capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true,"respond":"# + "\(respond)" + "}" + end + "}"
        return try! JSONDecoder().decode(SessionState.self, from: Data(json.utf8))
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
