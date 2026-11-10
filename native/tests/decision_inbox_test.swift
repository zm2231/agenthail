@testable import AgenthailHelpers
import Foundation

@main
struct DecisionInboxTest {
    static func main() {
        func question(_ text: String, suggested: Int?, ids: [String] = ["o0", "o1"]) -> SessionRequest.Question {
            SessionRequest.Question(question: text, header: text, multiSelect: false, options: [.init(id: ids[0], label: "Yes", description: ""), .init(id: ids[1], label: "No", description: "")], suggested: suggested)
        }
        let both = SessionRequest(id: "q", kind: "question", tool: "AskUserQuestion", questions: [question("Ship?", suggested: 0, ids: ["q0o0", "q0o1"]), question("Tag?", suggested: 1, ids: ["q1o0", "q1o1"])])
        check(DecisionInbox.recommended(both) == RequestDecision(choices: ["q0o0", "q1o1"]), "every question's suggestion becomes the recommended answer, by id")
        let partial = SessionRequest(id: "q", kind: "question", tool: "AskUserQuestion", questions: [question("Ship?", suggested: 0), question("Tag?", suggested: nil)])
        check(DecisionInbox.recommended(partial) == nil, "a question without a suggestion offers no recommended answer")
        let outOfRange = SessionRequest(id: "q", kind: "question", tool: "AskUserQuestion", questions: [question("Ship?", suggested: 5)])
        check(DecisionInbox.recommended(outOfRange) == nil, "a suggestion naming no option offers no recommended answer")
        let ask = SessionRequest(id: "a", kind: "ask", tool: "ask", title: "Which?", questions: [question("Which?", suggested: 1)])
        check(DecisionInbox.recommended(ask) == .choose("o1"), "an ask with a suggestion offers it")
        check(DecisionInbox.recommended(SessionRequest(id: "p", kind: "approval", tool: "Bash")) == nil, "an approval has no recommended answer")

        var delivery = DecisionItem(group: .waiting, sessionId: "target", projectId: nil, kind: "ask", reason: "Message not delivered", answerable: true)
        delivery.requests = [SessionRequest(id: "ask-0fca204fdeb3", kind: "ask", tool: "")]
        var shown = DecisionItem(group: .stopped, sessionId: "other", projectId: nil, kind: "ask", reason: "Message not delivered", answerable: false)
        shown.requests = delivery.requests
        let holding = DecisionInbox(decisions: [shown, delivery])
        check(holding.decision(answering: "ask-0fca204fdeb3")?.sessionId == "target", "a dead message's ask opens the person's decision that holds it")
        check(DecisionInbox(decisions: [shown]).decision(answering: "ask-0fca204fdeb3") == nil, "a decision the person cannot answer is not offered")
        check(holding.decision(answering: "ask-000000000009") == nil && holding.decision(answering: nil) == nil && holding.decision(answering: "") == nil, "an ask the person does not hold offers nothing")
        let dead = try! JSONDecoder().decode(QueueState.self, from: Data(#"{"id":7,"sessionId":"target","target":"@builder","message":"m","status":"dead","attempts":1,"queuedAt":"2026-11-10T06:00:00Z","evidence":"failed","askId":"ask-0fca204fdeb3"}"#.utf8))
        check(dead.askId == "ask-0fca204fdeb3", "a dead queue row names its ask")
        let message = try! JSONDecoder().decode(InboxMessage.self, from: Data(#"{"id":1,"sessionId":"target","target":"t","message":"m","status":"failed","evidence":"failed","pending":false,"createdAt":"2026-11-10T06:00:00Z","askId":"ask-0fca204fdeb3"}"#.utf8))
        check(message.askId == "ask-0fca204fdeb3", "an inbox message names its ask")
        check(DecisionInbox.recommended(SessionRequest(id: "e", kind: "question", tool: "AskUserQuestion", questions: [])) == nil, "a question with no questions has no recommended answer")

        let page = try! JSONDecoder().decode(DecisionsPage.self, from: Data(#"""
        {"decisions":[
          {"group":"waiting","sessionId":"w","kind":"request","reason":"r","since":"2026-11-08T10:00:00Z","answerable":true},
          {"group":"stopped","sessionId":"s","projectId":"p","kind":"stopped","reason":"r","answerable":false,"holdsUp":["x"]}
        ],"leadHandling":[{"sessionId":"l","heldBy":"lead","role":"worker","reason":"r"}]}
        """#.utf8))
        var inbox = DecisionInbox()
        inbox.add(page, host: "") { $0 }
        inbox.add(page, host: "h") { $0.contains("::") ? $0 : "h::" + $0 }
        check(inbox.decisions.map(\.sessionId) == ["h::s", "s", "h::w", "w"], "decisions from every computer are grouped, stopped first")
        let remote = inbox.decisions.first { $0.sessionId == "h::s" }!
        check(remote.hostID == "h" && remote.projectId == "h::p" && remote.holdsUp == ["h::x"], "a paired computer's ids are qualified with its host")
        check(inbox.decisions.first { $0.sessionId == "s" }?.hostID == "", "this computer's ids stay as they are")
        check(inbox.leadHandling.map(\.heldBy) == ["lead", "h::lead"], "the lead holding a prompt is qualified too")
        check(inbox.items(in: .holding).isEmpty && inbox.items(in: .waiting).count == 2, "items are read by group")
        check(inbox.removing("h::w").decisions.map(\.sessionId) == ["h::s", "s", "w"], "an answered session leaves the inbox")
        print("decision inbox ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
