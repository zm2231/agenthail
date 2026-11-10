@testable import AgenthailHelpers
import Foundation

@main
struct RequestSyncKeyTest {
    static func main() {
        let writable = session(readOnly: false)
        let readOnly = session(readOnly: true)
        expect(RequestSync.viewKey(writable) != RequestSync.viewKey(readOnly), "a session turning read-only resyncs its requests, which clears a request error")
        expect(RequestSync.viewKey(writable) == RequestSync.viewKey(session(readOnly: false)), "an unchanged session keeps its key")

        let row = #"{"id":"s1","surface":"claude","name":"Lead","status":"idle","queueCount":0,"open":true,"current":true,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true,"respond":false},"ask":{"id":"ask-447a922cae82","title":"Ready to ship","routedTo":"person","askedAt":"2026-11-10T06:26:27.31Z"},"attention":{"kind":"ask","reason":"Ready to ship"},"endTurn":{"headline":"Waiting on the deploy call","next":"Deploy once you say so","endedAt":"2026-11-10T06:26:30Z"}}"#
        let waiting = try! JSONDecoder().decode(SessionState.self, from: Data(row.utf8))
        expect(waiting.ask?.title == "Ready to ship" && waiting.endTurn?.headline == "Waiting on the deploy call" && waiting.pendingAskID == "ask-447a922cae82", "the row's waiting ask decodes: \(String(describing: waiting.ask))")
        expect(RequestSync.lists(waiting), "an ask is listed though the provider answers nothing through Agenthail")
        expect(!RequestSync.lists(session(respond: false)), "a session that answers nothing and has no ask lists nothing")
        expect(!RequestSync.lists(session(readOnly: true, respond: false, ask: ask("ask-000000000001"))), "a read-only session's ask is not listed")
        let newer = session(respond: false, ask: ask("ask-000000000002"))
        expect(RequestSync.cacheKey(waiting) != RequestSync.cacheKey(newer), "a new ask reads again")
        expect(RequestSync.cacheKey(waiting).hasPrefix("s1|"), "the key starts with the session, which keeps cards shown while it rereads")

        let answered = session(respond: false, ask: ask("ask-447a922cae82", answered: true))
        expect(answered.pendingAskID == nil && !RequestSync.lists(answered), "an answered ask is not fetched")
        expect(session(endTurn: #"{"headline":"Done","endedAt":"2026-11-06T04:52:30Z"}"#).pendingAskID == nil, "a turn with no ask has none")
        let routed = session(respond: false, ask: ask("ask-000000000003", to: "lead-session"))
        expect(routed.pendingAskID == nil && !RequestSync.lists(routed), "an ask routed to another session is not the person's")

        let pending = SessionRequest(id: "ask-447a922cae82", kind: "ask", tool: "")
        let approval = SessionRequest(id: "a1", kind: "approval", tool: "Bash")
        let listed = SessionRequests(permissionMode: "", requests: [approval, pending])
        expect(listed.offered(pendingAsk: "ask-447a922cae82").map(\.id) == ["a1", "ask-447a922cae82"], "the waiting ask follows provider requests")
        expect(listed.offered(pendingAsk: nil).map(\.id) == ["a1"], "an answered ask is never offered from a stale read")
        expect(listed.offered(pendingAsk: "ask-000000000002").map(\.id) == ["a1"], "a retired ask is never offered")
        expect(listed.offered(pendingAsk: "ask-447a922cae82", retired: "ask-447a922cae82").map(\.id) == ["a1"], "an ask answered here is not offered while the row catches up")

        var retired = RetiredAsks()
        retired.retire("ask-447a922cae82", in: "s1")
        expect(!retired.settles(waiting) && retired.retired(in: "s1") == "ask-447a922cae82", "the entry holds while the row still names the ask")
        expect(retired.retired(in: "other") == nil, "an entry belongs to its session")
        expect(!retired.settles(answered), "the entry holds once answered, until the turn moves on")
        var moved = retired
        moved.settle(newer)
        expect(moved.retired(in: "s1") == nil && listed.offered(pendingAsk: "ask-447a922cae82", retired: moved.retired(in: "s1")).count == 2, "a new ask drops the entry, so it cannot hide anything")
        var resumed = retired
        resumed.settle(session())
        expect(resumed.retired(in: "s1") == nil, "a turn that opens drops the entry")

        let question = SessionRequest.Question(question: "Deploy it now?", header: "", multiSelect: false, options: [])
        let asked = SessionRequest(id: "ask-447a922cae82", kind: "ask", tool: "", questions: [question])
        let receipt = AnswerReceipt(asked, RequestDecision(behavior: "allow", message: " After lunch. ", answers: ["Deploy it now?": "Wait"]))
        expect(receipt.text == "You answered: Wait · After lunch.", "the receipt says what was sent: \(receipt.text)")
        var answeredHere = RetiredAsks()
        answeredHere.retire("ask-447a922cae82", in: "s1", receipt: receipt)
        expect(answeredHere.receipt(for: waiting) == receipt && answeredHere.receipt(for: answered) == receipt, "the receipt shows while the row names the ask, answered or not")
        expect(answeredHere.receipt(for: newer) == nil && answeredHere.receipt(for: session()) == nil, "and goes when the turn moves on")
        expect(retired.receipt(for: waiting) == nil, "a refused answer leaves no receipt")
        let sent = RequestAnswerFailure(AgenthailAPIError.request(502, "session is gone"))
        expect(RequestAnswerFailure.retires(pending, failure: nil) && !RequestAnswerFailure.retires(pending, failure: sent) && !RequestAnswerFailure.retires(approval, failure: nil), "only a sent or terminally failed ask retires")
        print("request sync key tests passed")
    }

    static func ask(_ id: String, to routedTo: String = "person", answered: Bool = false) -> String {
        let answeredAt = answered ? #","answeredAt":"2026-11-10T06:30:02.5Z""# : ""
        return #"{"id":""# + id + #"","title":"Ready to ship","routedTo":""# + routedTo + #"","askedAt":"2026-11-10T06:26:27.31Z""# + answeredAt + "}"
    }

    static func session(readOnly: Bool = false, respond: Bool = true, ask: String? = nil, endTurn: String? = nil) -> SessionState {
        let fields = (ask.map { #","ask":"# + $0 } ?? "") + (endTurn.map { #","endTurn":"# + $0 } ?? "")
        let json = #"{"id":"s1","surface":"claude","name":"Lead","status":"idle","queueCount":0,"open":true,"current":true,"readOnly":"# + "\(readOnly)" + #","capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true,"respond":"# + "\(respond)" + "}" + fields + "}"
        return try! JSONDecoder().decode(SessionState.self, from: Data(json.utf8))
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
