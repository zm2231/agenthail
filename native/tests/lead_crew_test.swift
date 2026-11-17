@testable import AgenthailHelpers
import Foundation

@main
struct LeadCrewTest {
    static func session(_ id: String, status: String = "idle", waiting: Bool = false, lastActive: String? = nil, turn: String? = nil, reason: String? = nil, headline: String? = nil) -> SessionState {
        var row: [String: Any] = ["id": id, "surface": "claude", "name": id, "status": status, "queueCount": 0, "open": true, "current": false,
                                  "capabilities": ["send": true, "stream": true, "reply": true, "goal": false, "compact": false, "model": false, "interrupt": false, "steer": false, "respond": true]]
        if waiting { row["waitingOn"] = ["kind": "approval", "id": "r1", "answerable": true] }
        if let lastActive { row["lastActive"] = lastActive }
        if let turn { row["turnState"] = ["state": turn, "reason": reason ?? "", "source": "judge"] }
        if let headline { row["endTurn"] = ["headline": headline] }
        let data = try! JSONSerialization.data(withJSONObject: row)
        return try! JSONDecoder().decode(SessionState.self, from: data)
    }

    static func item(_ id: String, _ kind: String, _ title: String = "", role: String? = nil, text: String = "") -> TimelineItem {
        TimelineItem(id: id, kind: kind, role: role, title: title, text: text, timestamp: nil, callId: id, status: nil, truncated: false, truncationReason: nil, bodyRef: nil)
    }

    static func main() {
        let lead = session("lead")
        let worker = session("worker", status: "busy", lastActive: "2026-11-17T10:00:00Z")
        let helper = session("helper", waiting: true, lastActive: "2026-11-17T09:00:00Z")
        let nested = session("nested")
        let idle = session("idle", lastActive: "2026-11-17T11:00:00Z", turn: "waiting_person", headline: "Asked whether to ship")
        let family = SessionFamily(root: lead, members: [
            .init(session: worker, depth: 1, isWorker: true),
            .init(session: nested, depth: 2),
            .init(session: helper, depth: 1),
            .init(session: idle, depth: 1, isWorker: true),
        ], workingSubagents: 0)
        let tree = SessionTree(projects: [], needsYou: [], attention: [:], counts: [:], flat: [family])

        let crew = LeadCrew.crew(of: "lead", in: tree).map(\.id)
        check(crew == ["worker", "nested", "helper", "idle"], "a lead's crew is every member under it: \(crew)")
        check(LeadCrew.crew(of: "worker", in: tree).map(\.id) == ["nested"], "a member's crew is what nests under it")
        check(LeadCrew.crew(of: "helper", in: tree).isEmpty, "a member with nothing under it has no crew, so no Now tab")
        check(LeadCrew.crew(of: "elsewhere", in: tree).isEmpty, "an unknown session has no crew")

        let ordered = LeadCrew.ordered(LeadCrew.crew(of: "lead", in: tree)).map(\.id)
        check(ordered == ["helper", "worker", "idle", "nested"], "needs you first, then working, then the rest newest first: \(ordered)")

        check(LeadCrew.needsYou([helper, idle, worker]) == 1, "a label-only Needs you does not count toward needs you")
        check(LeadCrew.stateLabel(idle) == "Needs you", "an idle child reads its judged turn: \(LeadCrew.stateLabel(idle))")
        check(LeadCrew.stateLabel(session("w", turn: "waiting_work", reason: "@lead")) == "Waiting on @lead", "waiting on work names it")
        check(LeadCrew.stateLabel(session("c", turn: "continuing")) == "Said it would continue", "continuing")
        check(LeadCrew.stateLabel(session("d", turn: "done")) == "Done", "done")
        check(LeadCrew.stateLabel(helper) == SessionDisplayKind.label(helper), "a child waiting on a request reads as the row does")
        check(LeadCrew.stateLabel(session("plain")) == SessionDisplayKind.label(session("plain")), "no judged turn falls back to the row's status")
        check(LeadCrew.headline(idle) == "Asked whether to ship", "an idle child's headline is its last turn's")
        check(LeadCrew.headline(session("blank", headline: "")) == nil, "an empty headline is none")

        let steps = LeadCrew.stepsThisTurn([
            item("old", "toolCall", "Bash", text: #"{"command":"ls"}"#),
            item("prompt", "message", role: "user", text: "go"),
            item("read", "toolCall", "Read", text: #"{"file_path":"/repo/a.swift"}"#),
            item("read-out", "toolResult", "Read"),
            item("end", "toolCall", "mcp__agenthail__agenthail_end_turn"),
            item("run", "toolCall", "Bash", text: #"{"command":"make test"}"#),
        ])
        check(steps.count == 2 && steps[1].contains("make test"), "the lead's steps are this turn's calls, without end_turn: \(steps)")
        print("ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
