@testable import AgenthailHelpers
import Foundation

@main
struct AskCardTest {
    @MainActor
    static func main() {
        let json = #"{"permissionMode":"","requests":[{"id":"ask-447a922cae82","kind":"ask","tool":"","title":"Ready to ship","prompt":"Ready to ship","questions":[{"question":"Deploy it now?","header":"","multiSelect":false,"options":[{"id":"o0","label":"Deploy","description":""},{"id":"o1","label":"Wait","description":"hold until Monday"}],"suggested":0,"links":[{"title":"Runbook","url":"https://example.com/run"}]}],"actions":[{"id":"o0","title":"Deploy"},{"id":"o1","title":"Wait"},{"id":"reply","title":"Reply…"}],"alwaysAllow":false,"createdAt":"2026-11-06T04:52:30Z"}]}"#
        let decoded = try! JSONDecoder().decode(SessionRequests.self, from: Data(json.utf8))
        let request = decoded.requests[0]
        check(request.isAsk && request.usesPager && !request.isQuestion, "the contract's request decodes as an ask")
        let question = request.questions![0]
        check(question.suggested == 0 && question.links == [SessionRequest.Link(title: "Runbook", url: "https://example.com/run")], "suggested and links decode: \(question)")
        check(question.options[1].description == "hold until Monday" && question.options.map(\.id) == ["o0", "o1"], "an option's does is its description, and it carries its id")
        let plain = try! JSONDecoder().decode(SessionRequest.Question.self, from: Data(#"{"question":"Q","header":"","multiSelect":false,"options":[]}"#.utf8))
        check(plain.suggested == nil && plain.links == nil, "a question without them decodes")

        check(RequestCard.heading(request, asker: "@builder") == "@builder asks: Ready to ship", "the heading names the session, then asks:, then the title")
        check(RequestCard.heading(SessionRequest(id: "q", kind: "question", tool: "AskUserQuestion"), asker: "@builder", surface: "claude") == "Claude asks", "a question names its agent")
        check(RequestCard.heading(SessionRequest(id: "a", kind: "approval", tool: "shell"), asker: "@builder", surface: "codex") == "Codex wants your approval", "a Codex approval names Codex, not Claude")
        check(RequestCard.heading(SessionRequest(id: "a", kind: "approval", tool: "Bash"), asker: "@builder") == "The agent wants your approval", "an approval whose agent is unknown says the agent")

        var noted = QuestionPager(questions: [question], ask: true)
        noted.note = "After lunch."
        _ = noted.choose(1)
        let body = try! JSONSerialization.jsonObject(with: JSONEncoder().encode(noted.submit()!)) as! [String: Any]
        check(body["choices"] as? [String] == ["o1"] && body["message"] as? String == "After lunch." && body.count == 2, "the decision is the contract's payload: \(body)")
        var plainPick = QuestionPager(questions: [question], ask: true)
        _ = plainPick.choose(0)
        let noNote = try! JSONSerialization.jsonObject(with: JSONEncoder().encode(plainPick.submit()!)) as! [String: Any]
        check(noNote["message"] == nil && noNote["answers"] == nil && noNote["choices"] as? [String] == ["o0"], "an empty note and no typed answer are omitted: \(noNote)")

        let codex = try! JSONDecoder().decode(SessionRequests.self, from: Data(#"{"permissionMode":"","requests":[{"id":"c1","kind":"approval","tool":"shell","alwaysAllow":true,"alwaysDeny":true,"stoppable":true,"noReason":true,"actions":[{"id":"allow","title":"Allow"},{"id":"always","title":"Always allow"},{"id":"deny","title":"Deny"},{"id":"never","title":"Always deny"},{"id":"stop","title":"Deny and stop"}]}]}"#.utf8)).requests[0]
        let codexButtons = RequestCard.approvalButtons(codex.actions)
        check(codexButtons.allowing.map(\.id) == ["allow", "always"] && codexButtons.denying.map(\.id) == ["deny", "never", "stop"] && codexButtons.reply == nil, "a Codex approval shows the daemon's buttons and, taking no reason, no message: \(codexButtons)")
        check(codexButtons.denying.map(\.title) == ["Deny", "Always deny", "Deny and stop"], "button titles are the daemon's")
        let claude = SessionRequest(id: "a1", kind: "approval", tool: "Bash", actions: [.init(id: "allow", title: "Allow"), .init(id: "deny", title: "Deny"), .init(id: "reply", title: "Deny with message…")])
        let claudeButtons = RequestCard.approvalButtons(claude.actions)
        check(claudeButtons.allowing.map(\.id) == ["allow"] && claudeButtons.denying.map(\.id) == ["deny"] && claudeButtons.reply?.title == "Deny with message…", "a Claude approval offers a message with its denial: \(claudeButtons)")
        let legacy = try! JSONDecoder().decode(SessionRequests.self, from: Data(#"{"permissionMode":"","requests":[{"id":"r1","kind":"approval","tool":"Bash","title":"Claude wants to run true","alwaysAllow":true,"createdAt":"2026-10-20T00:00:00Z"}]}"#.utf8)).requests[0]
        let legacyButtons = RequestCard.approvalButtons(legacy.actions)
        check(legacy.actions.isEmpty && legacyButtons.allowing.isEmpty && legacyButtons.denying.isEmpty && legacyButtons.reply == nil, "an approval from a computer that sends no choices offers no buttons it could refuse")
        check(RequestCard.noChoicesNote.contains("answer it where the session runs"), "and the card says where to answer it instead")

        check(legacy.createdAt == "2026-10-20T00:00:00Z", "a request's time decodes")
        let stamped = RequestCard.timed("Claude wants your approval", legacy)
        check(stamped.hasPrefix("Claude wants your approval · ") && stamped.count > "Claude wants your approval · ".count, "the card's heading carries the request's time: \(stamped)")
        check(RequestCard.timed("Claude asks", SessionRequest(id: "n", kind: "question", tool: "AskUserQuestion")) == "Claude asks", "a request without a time keeps its plain heading")
        let reasons = try! JSONDecoder().decode(SessionRequests.self, from: Data(#"{"permissionMode":"","requests":[{"id":"p1","kind":"approval","tool":"Bash","reason":"Writes outside the workspace","alwaysAllow":false,"createdAt":"2026-11-08T10:00:00.123456Z"},{"id":"p2","kind":"approval","tool":"Edit","alwaysAllow":false},{"id":"q1","kind":"question","tool":"AskUserQuestion","alwaysAllow":false}]}"#.utf8))
        check(reasons.requests[0].reason == "Writes outside the workspace", "an approval's reason decodes")
        check(reasons.requests[0].createdAt.flatMap(TurnClock.time) != nil, "a time with fractional seconds reads")
        check(reasons.offered(pendingAsk: nil).map(\.id) == ["p1", "p2", "q1"], "every open request is offered, not only the first")
        let replyBody = try! JSONSerialization.jsonObject(with: JSONEncoder().encode(RequestDecision.choose("reply", message: "Use the staging host"))) as! [String: Any]
        check(replyBody["choices"] as? [String] == ["reply"] && replyBody["message"] as? String == "Use the staging host" && replyBody.count == 2, "a message denial is sent as the reply choice with its text: \(replyBody)")
        for (decision, wire) in [(RequestDecision.allow, ["allow"]), (.deny, ["deny"]), (.choose("always"), ["always"]), (.choose("stop"), ["stop"])] {
            let sent = try! JSONSerialization.jsonObject(with: JSONEncoder().encode(decision)) as! [String: Any]
            check(sent["choices"] as? [String] == wire && sent.count == 1, "an approval button sends only its id: \(sent)")
        }

        let stale = RequestAnswerFailure(AgenthailAPIError.requestStale("already answered"))
        check(stale.dropsCard && stale.message == RequestAnswerFailure.staleMessage, "409 stale drops the card")
        let maybe = RequestAnswerFailure(AgenthailAPIError.request(502, "the answer may have reached the session, so it is not sent again: timeout"))
        check(maybe.dropsCard && maybe.message == RequestAnswerFailure.maybeDeliveredMessage, "a 502 that may have arrived drops the card and is not retried")
        let open = RequestAnswerFailure(AgenthailAPIError.request(502, "session is not running"))
        check(!open.dropsCard && open.message == "session is not running", "a 502 that surely failed keeps the card to answer again")
        let elsewhere = RequestAnswerFailure(AgenthailAPIError.request(400, "may have reached the session"))
        check(!elsewhere.dropsCard, "only a 502 means delivery may have happened")

        let ledger = try! JSONDecoder().decode(SessionRequests.self, from: Data(#"{"permissionMode":"","requests":[{"id":"ask-1d2e","kind":"ask","tool":"","title":"W12: Ship on Friday?","deadline":"2026-11-10T15:40:00Z","questions":[{"question":"Ship on Friday?","header":"","multiSelect":false,"options":[{"id":"recommended","label":"Ship Friday","description":""},{"id":"default","label":"Hold","description":""}],"suggested":0}],"alwaysAllow":false}]}"#.utf8)).requests[0]
        check(ledger.deadline == "2026-11-10T15:40:00Z", "a ledger ask's deadline decodes")
        let due = TimestampParser.date("2026-11-10T15:40:00Z")!
        var utc = Calendar(identifier: .gregorian)
        utc.timeZone = TimeZone(identifier: "UTC")!
        check(RequestCard.deadline(ledger, now: due.addingTimeInterval(-3600), calendar: utc) == "Answer by \(due.formatted(date: .omitted, time: .shortened))", "a deadline today shows its time: \(String(describing: RequestCard.deadline(ledger, now: due, calendar: utc)))")
        check(RequestCard.deadline(ledger, now: due.addingTimeInterval(-3 * 86400), calendar: utc) == "Answer by \(due.formatted(date: .abbreviated, time: .shortened))", "a later deadline shows its date too")
        check(RequestCard.deadline(SessionRequest(id: "q", kind: "question", tool: "AskUserQuestion")) == nil, "a request without a deadline shows none")
        check(DecisionInbox.recommended(ledger) == .choose("recommended"), "accepting a ledger ask's recommendation sends its recommended option")
        print("ask card ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition { fatalError(message) }
    }
}
