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

        let projects = try! JSONDecoder().decode([ProjectState].self, from: Data(#"""
        [
          {"id":"repo:example/app","displayName":"app","checkouts":[
            {"host":{"id":"","name":"laptop","state":"local"},"projectId":"repo:example/app","rootPath":"/work/app"},
            {"host":{"id":"h","name":"server","state":"connected"},"projectId":"repo:example/app","rootPath":"/srv/app"}]},
          {"id":"notes-local","displayName":"notes","checkouts":[
            {"host":{"id":"","name":"laptop","state":"local"},"projectId":"notes-local","rootPath":"/work/notes"},
            {"host":{"id":"h","name":"server","state":"connected"},"projectId":"notes-server","rootPath":"/srv/notes"}]},
          {"id":"web","displayName":"web","linked":[{"id":"web-before","displayName":"web (old)"}],"checkouts":[
            {"host":{"id":"","name":"laptop","state":"local"},"projectId":"web","rootPath":"/work/web"},
            {"host":{"id":"h","name":"server","state":"connected"},"projectId":"web-server","rootPath":"/srv/web","linkedFrom":{"id":"web-before","displayName":"web (old)"}}]}
        ]
        """#.utf8))
        func decision(_ session: String, project: String?, host: String) -> DecisionItem {
            DecisionInbox.qualified(DecisionItem(group: .waiting, sessionId: session, projectId: project, kind: "ask", reason: "r", answerable: false), host: host) { id in
                host.isEmpty || id.contains("::") ? id : host + "::" + id
            }
        }
        let local = decision("l", project: "repo:example/app", host: "")
        let keyed = decision("k", project: "repo:example/app", host: "h")
        let folder = decision("f", project: "notes-server", host: "h")
        let linkedLocal = decision("wl", project: "web", host: "")
        let linkedRemote = decision("wr", project: "web-before", host: "h")
        let unknown = decision("u", project: "elsewhere", host: "h")
        let otherHost = decision("o", project: "notes-server", host: "k")
        check(DecisionInbox.projectID(of: local, in: projects) == "repo:example/app" && DecisionInbox.projectID(of: keyed, in: projects) == "repo:example/app", "a project with a key is one tile on every computer")
        check(DecisionInbox.projectID(of: folder, in: projects) == "notes-local", "a paired computer's keyless project reaches the tile holding its checkout")
        check(DecisionInbox.projectID(of: linkedRemote, in: projects) == "web" && DecisionInbox.projectID(of: linkedLocal, in: projects) == "web", "a project linked across two computers: the remote's own id lands on the same tile as the local one")
        check(DecisionInbox.projectID(of: unknown, in: projects) == nil && DecisionInbox.projectID(of: otherHost, in: projects) == nil, "a project no tile holds, or another computer's checkout, maps to no tile")
        let mixed = DecisionInbox(decisions: [local, keyed, folder, linkedLocal, linkedRemote, unknown, otherHost])
        check(mixed.needsYou(in: projects) == ["repo:example/app": 2, "notes-local": 1, "web": 2] && mixed.decisions.count == 7, "tile badges count their decisions and the Inbox still counts the ones no tile holds")
        print("decision inbox ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
