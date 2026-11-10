@testable import AgenthailHelpers
import Foundation

@main
struct SessionAttentionTest {
    static func main() {
        let caps = Capabilities(send: true, stream: true, reply: true, goal: false, compact: false, model: true, interrupt: true, steer: true)
        func session(_ id: String, status: String, lastActive: String, alias: String? = nil) -> SessionState {
            SessionState(id: id, surface: "codex", name: id, alias: alias, status: status, lastActive: lastActive, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: caps, readOnly: nil, readOnlyReason: nil, cwd: "/repo/main", project: ProjectIdentity(id: "agenthail", displayName: "agenthail", rootPath: "/repo", kind: "git"))
        }
        func waiting(_ id: String, kind: String, lastActive: String = "2026-10-04T11:00:00Z") -> SessionState {
            var state = session(id, status: "idle", lastActive: lastActive)
            state.waitingOn = WaitingState(kind: kind, id: "req-\(id)")
            return state
        }
        func delivery(_ id: String, routedTo: String) -> SessionState {
            var state = session(id, status: "idle", lastActive: "2026-10-04T11:30:00Z")
            state.ask = SessionAsk(id: "ask-\(id)", title: "Message not delivered", routedTo: routedTo)
            return state
        }
        let asker = waiting("asker", kind: "question", lastActive: "2026-10-01T00:00:00Z")
        let approver = waiting("approver", kind: "approval")
        let idle = session("idle", status: "idle", lastActive: "2026-10-04T11:59:00Z")
        let target = delivery("target", routedTo: "person")
        let sender = delivery("sender", routedTo: "sender")
        var archivedAsker = waiting("archived-asker", kind: "question")
        archivedAsker.archived = true
        var coldTarget = delivery("cold-target", routedTo: "person")
        coldTarget.status = "notLoaded"
        var lostRemote = waiting("h1::lost", kind: "question")
        lostRemote.host = SessionHost(id: "h1", name: "vps.example.ts.net", state: "disconnected")
        lostRemote.unavailableReason = "The host is offline"
        var both = waiting("both", kind: "input")
        both.ask = SessionAsk(id: "ask-both", title: "Message not delivered", routedTo: "person")
        let reasons = SessionAttention.build(sessions: [asker, approver, idle, target, sender, archivedAsker, coldTarget, lostRemote, both])
        expect(reasons["asker"] == SessionAttention(kind: .request, reason: "Asks you a question"), "a session waiting on a question needs you")
        expect(reasons["approver"]?.reason == "Needs your approval", "an approval reads as one")
        expect(reasons["target"] == SessionAttention(kind: .request, reason: "Asks: Message not delivered"), "a delivery problem the person answers reads as its ask")
        expect(reasons["sender"] == nil, "a delivery problem routed to the agent that sent it is the agent's, not the person's")
        expect(reasons["both"]?.reason == "Needs your input", "the provider's own request outranks an ask")
        expect(reasons["archived-asker"] == nil && reasons["cold-target"] == nil && reasons["h1::lost"] == nil, "archived, unavailable and unreachable sessions never need you")
        expect(Set(reasons.keys) == ["asker", "approver", "target", "both"], "only sessions that can act are flagged: \(reasons.keys.sorted())")

        func hostedSession(_ id: String, waiting: WaitingState) -> SessionState {
            var state = SessionState(id: id, surface: "claude", name: id, alias: nil, status: "busy", lastActive: "2026-10-04T11:00:00Z", queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(respond: true, lifecycle: SessionLifecycle(stop: true, resume: true)), readOnly: nil, readOnlyReason: nil)
            state.runtimeKind = "claude-host"
            state.waitingOn = waiting
            return state
        }
        let hostedWait = hostedSession("hosted", waiting: WaitingState(kind: "approval", id: "r0"))
        let held = hostedSession("held", waiting: WaitingState(kind: "approval", id: "r1", answerable: true))
        var unknownReset = session("unknown-reset", status: "idle", lastActive: "2026-10-04T11:39:00Z")
        unknownReset.usageLimit = UsageLimitState()
        var resuming = session("resuming", status: "idle", lastActive: "2026-10-04T11:38:00Z")
        resuming.usageLimit = UsageLimitState(resetsAt: "2026-10-04T18:20:00Z")
        resuming.limitResume = LimitResumeState(state: "waiting", resetsAt: "2026-10-04T18:20:00Z")
        var probing = session("probing", status: "idle", lastActive: "2026-10-04T11:37:00Z")
        probing.limitResume = LimitResumeState(state: "waiting")
        var kept = session("kept", status: "idle", lastActive: "2026-10-04T11:36:00Z")
        kept.limitResume = LimitResumeState(state: "canceled", resetsAt: "2026-10-04T18:20:00Z")
        var refused = session("refused", status: "idle", lastActive: "2026-10-04T11:35:00Z")
        refused.turnStop = TurnStopState(turnId: "u1", reason: "Claude API error: flagged", refused: true)
        var errored = session("errored", status: "idle", lastActive: "2026-10-04T11:34:00Z")
        errored.turnStop = TurnStopState(turnId: "t1", reason: "Codex error: exceeded retry limit")
        var retrying = session("retrying", status: "busy", lastActive: "2026-10-04T11:33:00Z")
        retrying.turnStop = errored.turnStop
        let places = SessionAttention.build(sessions: [hostedWait, held, unknownReset, resuming, probing, kept, refused, errored, retrying])
        expect(places["refused"] == SessionAttention(kind: .stopped, reason: "Safeguards refused this conversation; start a new one"), "a refused conversation says to start a new one")
        expect(places["errored"] == SessionAttention(kind: .stopped, reason: "Codex error: exceeded retry limit"), "a turn an API error ended names the error")
        expect(places["retrying"] == nil, "a new turn clears the stop")
        expect(places["resuming"]?.kind == .usageLimit && places["resuming"]?.reason.hasPrefix("Usage limit, resumes ") == true, "a stop its computer resumes says when")
        expect(places["probing"]?.reason == "Usage limit, resumes when it lifts", "a stop with no reset time resumes when the limit lifts")
        expect(places["kept"]?.reason == "Stopped by usage limit", "a stop kept stopped says so")
        expect(places["hosted"]?.reason == "Waiting on a prompt Agenthail can't reach. Stop and resume to clear it", "a hosted prompt no one holds says how to clear it")
        expect(places["held"]?.reason == "Needs your approval", "a request Agenthail holds asks for the answer")
        expect(places["unknown-reset"]?.reason == "Usage limit reached", "a limit with no reset time says it was reached")

        var asking = session("asking", status: "idle", lastActive: "2026-10-04T11:40:00Z")
        asking.ask = SessionAsk(id: "ask-000000000001", title: "Ready to ship", routedTo: "person")
        asking.endTurn = SessionEndTurn(headline: "Waiting on the deploy call")
        var answered = session("answered", status: "idle", lastActive: "2026-10-04T11:41:00Z")
        answered.ask = SessionAsk(id: "ask-000000000002", title: "Ready to ship", routedTo: "person", answeredAt: "2026-10-04T11:42:00Z")
        var told = session("told", status: "idle", lastActive: "2026-10-04T11:43:00Z")
        told.ask = SessionAsk(id: "ask-000000000003", title: "Ready", routedTo: "lead")
        let asks = SessionAttention.build(sessions: [asking, answered, told])
        expect(asks["asking"] == SessionAttention(kind: .request, reason: "Asks: Ready to ship"), "an ask for the person needs the person, named by its title: \(String(describing: asks["asking"]))")
        expect(asks["answered"] == nil && asks["told"] == nil, "an answered ask, or one routed to another session, does not")
        print("session attention tests passed")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
