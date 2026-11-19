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
        expect(listed.offered(pendingAsk: "ask-447a922cae82", retired: ["ask-447a922cae82"]).map(\.id) == ["a1"], "an ask answered here is not offered while the row catches up")

        let older = SessionRequest(id: "ask-447a922cae82", kind: "ask", tool: "")
        let later = SessionRequest(id: "ask-b684039d968c", kind: "ask", tool: "")
        let third = SessionRequest(id: "ask-c0ffee000003", kind: "ask", tool: "")
        let both = SessionRequests(permissionMode: "", requests: [approval, older, later])
        let twoOpen = session(respond: false, ask: ask("ask-447a922cae82", open: ["ask-447a922cae82", "ask-b684039d968c"]))
        expect(twoOpen.pendingAsks == ["ask-447a922cae82", "ask-b684039d968c"], "the row's open asks decode: \(String(describing: twoOpen.pendingAsks))")
        expect(both.offered(pendingAsk: twoOpen.pendingAskID, open: twoOpen.pendingAsks).map(\.id) == ["a1", "ask-447a922cae82", "ask-b684039d968c"], "every open ask is offered, not only the row's oldest")
        expect(both.offered(pendingAsk: twoOpen.pendingAskID, open: twoOpen.pendingAsks, retired: ["ask-b684039d968c"]).map(\.id) == ["a1", "ask-447a922cae82"], "a newer ask answered here is not offered again")
        expect(both.offered(pendingAsk: waiting.pendingAskID, open: waiting.pendingAsks).map(\.id) == ["a1", "ask-447a922cae82"], "a row from an older daemon offers only its own ask")

        let answeredElsewhere = session(respond: false, ask: ask("ask-447a922cae82", open: ["ask-447a922cae82"]))
        expect(RequestSync.cacheKey(answeredElsewhere) != RequestSync.cacheKey(twoOpen), "a newer ask answered elsewhere rereads the requests")
        expect(both.offered(pendingAsk: answeredElsewhere.pendingAskID, open: answeredElsewhere.pendingAsks).map(\.id) == ["a1", "ask-447a922cae82"], "and is not offered from the read before it")
        let threeOpen = session(respond: false, ask: ask("ask-447a922cae82", open: ["ask-447a922cae82", "ask-b684039d968c", "ask-c0ffee000003"]))
        expect(RequestSync.cacheKey(threeOpen) != RequestSync.cacheKey(twoOpen), "a third ask arriving rereads the requests")
        let all = SessionRequests(permissionMode: "", requests: [older, later, third])
        expect(all.offered(pendingAsk: threeOpen.pendingAskID, open: threeOpen.pendingAsks).count == 3, "and it is offered")
        let sameMillisecond = session(respond: false, ask: ask("ask-b684039d968c", open: ["ask-b684039d968c"]))
        expect(both.offered(pendingAsk: sameMillisecond.pendingAskID, open: sameMillisecond.pendingAsks).map(\.id) == ["a1", "ask-b684039d968c"], "an ask the row no longer lists is never offered from a stale read, whatever its time")
        expect(answered.pendingAsks == nil && session(respond: false, ask: ask("ask-447a922cae82", answered: true, open: ["ask-447a922cae82"])).pendingAsks == nil, "an answered row ask lists nothing")

        var retired = RetiredAsks()
        retired.retire("ask-447a922cae82", in: "s1")
        expect(!retired.settles(waiting) && retired.retired(in: "s1") == ["ask-447a922cae82"], "the entry holds while the row still names the ask")
        expect(retired.retired(in: "other").isEmpty, "an entry belongs to its session")
        expect(!retired.settles(answered), "the entry holds once answered, until the turn moves on")
        var settled = retired
        settled.settle(newer)
        expect(settled.retired(in: "s1").isEmpty && listed.offered(pendingAsk: "ask-447a922cae82", retired: settled.retired(in: "s1")).count == 2, "a new ask drops the entry, so it cannot hide anything")
        var resumed = retired
        resumed.settle(session())
        expect(resumed.retired(in: "s1").isEmpty, "a turn that opens drops the entry")
        var two = RetiredAsks()
        two.retire("ask-447a922cae82", in: "s1")
        two.retire("ask-b684039d968c", in: "s1")
        expect(all.offered(pendingAsk: threeOpen.pendingAskID, open: threeOpen.pendingAsks, retired: two.retired(in: "s1")).map(\.id) == ["ask-c0ffee000003"], "two asks answered here both stay hidden while a stale read lists them, and the third is still offered")
        expect(!two.settles(threeOpen), "both entries hold while the row still names both")
        let laterOnly = session(respond: false, ask: ask("ask-b684039d968c", open: ["ask-b684039d968c", "ask-c0ffee000003"]))
        var one = two
        one.settle(laterOnly)
        expect(one.retired(in: "s1") == ["ask-b684039d968c"], "the row dropping one ask drops only its entry: \(one.retired(in: "s1"))")

        let question = SessionRequest.Question(question: "Deploy it now?", header: "", multiSelect: false, options: [SessionRequest.Option(id: "o0", label: "Deploy", description: ""), SessionRequest.Option(id: "o1", label: "Wait", description: "")])
        let asked = SessionRequest(id: "ask-447a922cae82", kind: "ask", tool: "", questions: [question])
        let receipt = AnswerReceipt(asked, RequestDecision(choices: ["o1"], message: " After lunch. "))
        expect(receipt.text == "You answered: Wait · After lunch.", "the receipt names the picked option by its label: \(receipt.text)")
        let typedReceipt = AnswerReceipt(asked, RequestDecision(choices: [], answers: ["Deploy it now?": "After the demo"]))
        expect(typedReceipt.text == "You answered: After the demo", "a typed answer reads as typed: \(typedReceipt.text)")
        var answeredHere = RetiredAsks()
        answeredHere.retire("ask-447a922cae82", in: "s1", receipt: receipt)
        expect(answeredHere.receipt(for: waiting) == receipt && answeredHere.receipt(for: answered) == receipt, "the receipt shows while the row names the ask, answered or not")
        expect(answeredHere.receipt(for: newer) == nil && answeredHere.receipt(for: session()) == nil, "and goes when the turn moves on")
        expect(retired.receipt(for: waiting) == nil, "a refused answer leaves no receipt")
        var twoAnswers = RetiredAsks()
        let secondAsked = SessionRequest(id: "ask-b684039d968c", kind: "ask", tool: "", questions: [question])
        let secondReceipt = AnswerReceipt(secondAsked, RequestDecision(choices: ["o0"], message: ""))
        twoAnswers.retire("ask-447a922cae82", in: "s1", receipt: receipt)
        twoAnswers.retire("ask-b684039d968c", in: "s1", receipt: secondReceipt)
        let namingSecond = session(respond: false, ask: ask("ask-b684039d968c", open: ["ask-447a922cae82", "ask-b684039d968c"]))
        expect(twoAnswers.receipt(for: namingSecond) == secondReceipt && twoAnswers.receipt(for: twoOpen) == nil, "the receipt is the latest answer's, shown while the row names that ask")
        twoAnswers.settle(session(respond: false, ask: ask("ask-447a922cae82", open: ["ask-447a922cae82"])))
        expect(twoAnswers.retired(in: "s1") == ["ask-447a922cae82"] && twoAnswers.receipt(for: twoOpen) == nil, "settling away the latest ask drops its receipt and keeps the other entry")
        let sent = RequestAnswerFailure(AgenthailAPIError.request(502, "session is gone"))
        expect(RequestAnswerFailure.retires(pending, failure: nil) && !RequestAnswerFailure.retires(pending, failure: sent) && !RequestAnswerFailure.retires(approval, failure: nil), "only a sent or terminally failed ask retires")
        print("request sync key tests passed")
    }

    static func ask(_ id: String, to routedTo: String = "person", answered: Bool = false, open: [String]? = nil) -> String {
        let answeredAt = answered ? #","answeredAt":"2026-11-10T06:30:02.5Z""# : ""
        let listed = open.map { #","open":["# + $0.map { "\"\($0)\"" }.joined(separator: ",") + "]" } ?? ""
        return #"{"id":""# + id + #"","title":"Ready to ship","routedTo":""# + routedTo + #"","askedAt":"2026-11-10T06:26:27.31Z""# + answeredAt + listed + "}"
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
