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

        let parked = try! JSONDecoder().decode(DecisionsPage.self, from: Data(#"""
        {"decisions":[
          {"group":"waiting","sessionId":"w","kind":"request","reason":"r","since":"2026-11-08T10:00:00Z","answerable":true},
          {"group":"waiting","sessionId":"w","kind":"parked","workItem":"widget-W12","reason":"widget-W12 waits on you: titles only?","since":"2026-11-08T10:00:00Z","answerable":false}
        ],"leadHandling":[]}
        """#.utf8))
        var withParked = DecisionInbox()
        withParked.add(parked, host: "") { $0 }
        check(withParked.decisions.map(\.key) == ["w", "w#widget-W12"], "a parked item and its session's own request are two rows")
        check(withParked.removing("w").decisions.map(\.key) == ["w#widget-W12"], "answering the session leaves its parked item waiting on the person")
        check(withParked.decisions.last?.reason == "titles only?", "an older computer's work-item prefix is dropped from the question")

        let own = try! JSONDecoder().decode(DecisionsPage.self, from: Data(#"""
        {"decisions":[
          {"group":"waiting","sessionId":"cd55","projectId":"p","kind":"parked","workItem":"widget-W8","workTitle":"Search titles","reason":"Titles only?","since":"2026-11-08T10:00:00Z","answerable":false},
          {"group":"waiting","sessionId":"cd55","projectId":"p","kind":"parked","workItem":"widget-W9","workTitle":"Search bodies","reason":"Titles only?","since":"2026-11-08T10:01:00Z","answerable":false},
          {"group":"waiting","sessionId":"cd55","kind":"ask","reason":"Ship?","since":"2026-11-08T10:02:00Z","answerable":true},
          {"group":"waiting","sessionId":"cd66","kind":"ask","reason":"Ship?","since":"2026-11-08T10:03:00Z","answerable":true}
        ],"leadHandling":[]}
        """#.utf8))
        let paired = try! JSONDecoder().decode(DecisionsPage.self, from: Data(#"""
        {"decisions":[
          {"group":"waiting","sessionId":"mac::cd55","projectId":"mac::p","kind":"parked","workItem":"widget-W8","reason":"widget-W8 waits on you: Titles only?","since":"2026-11-08T09:00:00Z","answerable":false},
          {"group":"waiting","sessionId":"mac::cd55","kind":"parked","workItem":"widget-W10","workTitle":"Search tags","reason":"widget-W10 waits on you: Titles only?","since":"2026-11-08T10:05:00Z","answerable":false},
          {"group":"stopped","sessionId":"f1","kind":"stopped","reason":"r","answerable":false,"holdsUp":["mac::cd55"]}
        ],"leadHandling":[{"sessionId":"f2","heldBy":"mac::lead","role":"worker","reason":"r"}]}
        """#.utf8))
        let qualifyForge: (String) -> String = { $0.contains("::") ? $0 : "forge::" + $0 }
        for order in [[("", own), ("forge", paired)], [("forge", paired), ("", own)]] {
            var merged = DecisionInbox()
            for (host, page) in order { merged.add(page, host: host, own: "mac", qualify: host.isEmpty ? { $0 } : qualifyForge) }
            let card = merged.decisions.first { $0.isParked }!
            check(merged.decisions.filter(\.isParked).count == 1, "parked work on one question from one session is one card: \(merged.decisions.map(\.id))")
            check(card.coveredWork.map(\.workItem) == ["widget-W8", "widget-W9", "widget-W10"], "the card covers every item once: \(card.coveredWork.map(\.workItem))")
            check(card.coveredWork.first?.hostID == "" && card.coveredWork.first?.workTitle == "Search titles", "this computer's copy of an item wins over a paired computer's")
            check(card.sessionId == "cd55" && card.coveredWork.allSatisfy { $0.sessionId == "cd55" }, "a session a paired computer names with this computer's id reads as local")
            check(merged.decisions.filter { $0.kind == "ask" }.count == 2, "asks are never merged")
            check(merged.decisions.first { $0.sessionId == "forge::f1" }?.holdsUp == ["cd55"], "held-up sessions are localised too")
            check(merged.leadHandling.first?.heldBy == "lead", "a lead on this computer is localised")
            check(DecisionInbox.tag(card, sentTo: nil) == "Covers 3 items" && DecisionInbox.tag(card, sentTo: "@w") == "Covers 3 items · Sent to @w", "a grouped card says what it covers and who was answered")
            check(DecisionInbox.reply("titles", for: card) == "Answer for widget-W8, widget-W9, widget-W10: titles", "the reply names every covered item")
        }
        var resolved = DecisionInbox()
        var later = own
        later.decisions.removeAll { $0.workItem == "widget-W8" }
        resolved.add(later, host: "", own: "mac") { $0 }
        var before = DecisionInbox()
        before.add(own, host: "", own: "mac") { $0 }
        check(resolved.decisions.first(where: \.isParked)?.id == before.decisions.first(where: \.isParked)?.id, "a card keeps its identity when its first covered item leaves")
        var mixed = DecisionInbox()
        mixed.add(own, host: "", own: "mac") { $0 }
        mixed.add(paired, host: "forge", own: "mac", qualify: qualifyForge)
        let hosts = DecisionInbox.dismissals(of: mixed.decisions.first(where: \.isParked)!).map { "\($0.workItem ?? ""):\($0.host)" }
        check(hosts == ["widget-W8:", "widget-W9:", "widget-W10:forge"], "each covered item is dismissed on the computer that reported it: \(hosts)")
        check(DecisionInbox.localised("macx::a", own: "mac") == "macx::a" && DecisionInbox.localised("mac::a", own: "") == "mac::a", "only this computer's own prefix is dropped")
        print("decision inbox ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
