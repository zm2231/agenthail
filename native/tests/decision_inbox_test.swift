@testable import AgenthailHelpers
import Foundation

@main
struct DecisionInboxTest {
    static func main() {
        func question(_ text: String, suggested: Int?) -> SessionRequest.Question {
            SessionRequest.Question(question: text, header: text, multiSelect: false, options: [.init(label: "Yes", description: ""), .init(label: "No", description: "")], suggested: suggested)
        }
        let both = SessionRequest(id: "q", kind: "question", tool: "AskUserQuestion", questions: [question("Ship?", suggested: 0), question("Tag?", suggested: 1)])
        check(DecisionInbox.recommended(both) == .answers(["Ship?": "Yes", "Tag?": "No"]), "every question's suggestion becomes the recommended answer")
        let partial = SessionRequest(id: "q", kind: "question", tool: "AskUserQuestion", questions: [question("Ship?", suggested: 0), question("Tag?", suggested: nil)])
        check(DecisionInbox.recommended(partial) == nil, "a question without a suggestion offers no recommended answer")
        let outOfRange = SessionRequest(id: "q", kind: "question", tool: "AskUserQuestion", questions: [question("Ship?", suggested: 5)])
        check(DecisionInbox.recommended(outOfRange) == nil, "a suggestion naming no option offers no recommended answer")
        let ask = SessionRequest(id: "a", kind: "ask", tool: "ask", title: "Which?", questions: [question("Which?", suggested: 1)])
        check(DecisionInbox.recommended(ask) == .answers(["Which?": "No"]), "an ask with a suggestion offers it")
        check(DecisionInbox.recommended(SessionRequest(id: "p", kind: "approval", tool: "Bash")) == nil, "an approval has no recommended answer")
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
