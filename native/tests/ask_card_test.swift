@testable import AgenthailHelpers
import Foundation

@main
struct AskCardTest {
    @MainActor
    static func main() {
        let json = #"{"permissionMode":"","requests":[{"id":"turn-end-1793940750123","kind":"ask","tool":"","title":"Ready to ship","prompt":"Ready to ship","questions":[{"question":"Deploy it now?","header":"","multiSelect":false,"options":[{"label":"Deploy","description":""},{"label":"Wait","description":"hold until Monday"}],"suggested":0,"links":[{"title":"Runbook","url":"https://example.com/run"}]}],"actions":[{"id":"o0","title":"Deploy"},{"id":"o1","title":"Wait"},{"id":"reply","title":"Reply…"}],"alwaysAllow":false,"createdAt":"2026-11-06T04:52:30Z"}]}"#
        let decoded = try! JSONDecoder().decode(SessionRequests.self, from: Data(json.utf8))
        let request = decoded.requests[0]
        check(request.isAsk && request.usesPager && !request.isQuestion, "the contract's request decodes as an ask")
        let question = request.questions![0]
        check(question.suggested == 0 && question.links == [SessionRequest.Link(title: "Runbook", url: "https://example.com/run")], "suggested and links decode: \(question)")
        check(question.options[1].description == "hold until Monday", "an option's does is its description")
        let plain = try! JSONDecoder().decode(SessionRequest.Question.self, from: Data(#"{"question":"Q","header":"","multiSelect":false,"options":[]}"#.utf8))
        check(plain.suggested == nil && plain.links == nil, "a question without them decodes")

        check(RequestCard.heading(request, asker: "@builder") == "@builder asks: Ready to ship", "the heading names the session, then asks:, then the title")
        check(RequestCard.heading(SessionRequest(id: "q", kind: "question", tool: "AskUserQuestion"), asker: "@builder") == "Claude asks", "a question keeps its heading")

        let body = try! JSONSerialization.jsonObject(with: JSONEncoder().encode(RequestDecision(behavior: "allow", message: "After lunch.", answers: ["Deploy it now?": "Wait"]))) as! [String: Any]
        check(body["behavior"] as? String == "allow" && body["message"] as? String == "After lunch." && (body["answers"] as? [String: String]) == ["Deploy it now?": "Wait"] && body.count == 3, "the decision is the contract's payload: \(body)")
        let noNote = try! JSONSerialization.jsonObject(with: JSONEncoder().encode(QuestionPager(questions: [question], ask: true).decision(["Deploy it now?": "Deploy"]))) as! [String: Any]
        check(noNote["message"] == nil, "an empty note is omitted from the payload: \(noNote)")

        let stale = RequestAnswerFailure(AgenthailAPIError.requestStale("already answered"))
        check(stale.dropsCard && stale.message == RequestAnswerFailure.staleMessage, "409 stale drops the card")
        let maybe = RequestAnswerFailure(AgenthailAPIError.request(502, "the answer may have reached the session, so it is not sent again: timeout"))
        check(maybe.dropsCard && maybe.message == RequestAnswerFailure.maybeDeliveredMessage, "a 502 that may have arrived drops the card and is not retried")
        let open = RequestAnswerFailure(AgenthailAPIError.request(502, "session is not running"))
        check(!open.dropsCard && open.message == "session is not running", "a 502 that surely failed keeps the card to answer again")
        let elsewhere = RequestAnswerFailure(AgenthailAPIError.request(400, "may have reached the session"))
        check(!elsewhere.dropsCard, "only a 502 means delivery may have happened")
        print("ask card ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition { fatalError(message) }
    }
}
