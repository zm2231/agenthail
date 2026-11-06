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
        func item(_ id: Int64, _ sessionID: String, _ reason: String) -> AttentionState {
            AttentionState(id: id, sessionId: sessionID, target: sessionID, queueId: id, reason: reason, requestedAction: "Retry or cancel this message", createdAt: "2026-10-04 11:00:00")
        }
        func problem(_ id: Int64, to target: String, from sender: String?) -> DeliveryProblem {
            DeliveryProblem(deliveryId: id, sessionId: target, sourceSessionId: sender, attentionSessionId: sender, message: "Run the tests", reason: "expired", status: "failed", at: "2026-10-04 11:00:00")
        }
        let asker = waiting("asker", kind: "question", lastActive: "2026-10-01T00:00:00Z")
        let approver = waiting("approver", kind: "approval")
        let reviewer = session("reviewer", status: "idle", lastActive: "2026-10-04T11:30:00Z", alias: "reviewer")
        let sender = session("sender", status: "idle", lastActive: "2026-10-04T11:59:00Z")
        let fanout = session("fanout", status: "idle", lastActive: "2026-10-04T11:58:00Z")
        let target = session("target", status: "idle", lastActive: "2026-10-04T11:57:00Z")
        let mixedTarget = session("mixed", status: "idle", lastActive: "2026-10-04T11:56:00Z")
        var archivedAsker = waiting("archived-asker", kind: "question")
        archivedAsker.archived = true
        var coldTarget = session("cold-target", status: "notLoaded", lastActive: "2026-09-12T00:00:00Z")
        coldTarget.waitingOn = WaitingState(kind: "question", id: "stale")
        var lostRemote = waiting("h1::lost", kind: "question")
        lostRemote.host = SessionHost(id: "h1", name: "vps.example.ts.net", state: "disconnected")
        lostRemote.unavailableReason = "The host is offline"
        let both = waiting("both", kind: "input")
        let pool = [asker, approver, reviewer, sender, fanout, target, mixedTarget, archivedAsker, coldTarget, lostRemote, both]
        let reasons = SessionAttention.build(
            sessions: pool,
            attention: [item(1, "target", "Delivery outcome could not be confirmed"), item(2, "target", "Delivery outcome could not be confirmed"), item(3, "mixed", "Claude needs to reconnect"), item(4, "mixed", "The target refused to start"), item(5, "cold-target", "Delivery target is unavailable"), item(6, "gone", "Delivery target is unavailable")],
            deliveryProblems: [problem(1, to: "reviewer", from: "sender"), problem(2, to: "reviewer", from: "sender"), problem(3, to: "reviewer", from: "fanout"), problem(4, to: "target", from: "fanout"), problem(5, to: "reviewer", from: nil), problem(6, to: "reviewer", from: "both")]
        )
        expect(reasons["asker"] == SessionAttention(kind: .request, reason: "Asks you a question"), "a session waiting on a question needs you even with no queue attention")
        expect(reasons["approver"]?.reason == "Needs your approval", "an approval reads as one")
        expect(reasons["target"] == SessionAttention(kind: .incoming, reason: "2 messages: Delivery outcome could not be confirmed"), "failed messages to a session share their reason")
        expect(reasons["mixed"]?.reason == "2 messages need a decision", "different reasons are counted, not listed")
        expect(reasons["sender"] == SessionAttention(kind: .outgoing, reason: "2 messages to @reviewer not delivered"), "an agent's undelivered messages name their target")
        expect(reasons["fanout"]?.reason == "2 messages not delivered", "messages to several targets are counted")
        expect(reasons["both"]?.kind == .request && reasons["both"]?.reason == "Needs your input", "a request outranks a delivery problem")
        expect(reasons["archived-asker"] == nil && reasons["cold-target"] == nil && reasons["h1::lost"] == nil && reasons["gone"] == nil, "archived, unavailable, unreachable and missing sessions never need you")
        expect(reasons["reviewer"] == nil, "the target of an agent's failed message is not flagged; a person's own failed send flags no one")
        expect(Set(reasons.keys) == ["asker", "approver", "target", "mixed", "sender", "fanout", "both"], "only sessions that can act are flagged: \(reasons.keys.sorted())")

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
        let places = SessionAttention.build(sessions: [hostedWait, held, unknownReset, resuming, probing, kept, refused, errored, retrying], attention: [], deliveryProblems: [])
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
        asking.endTurn = SessionEndTurn(headline: "Ready to ship", needs: "you", requestId: "turn-end-1")
        var answered = session("answered", status: "idle", lastActive: "2026-10-04T11:41:00Z")
        answered.endTurn = SessionEndTurn(headline: "Ready to ship", needs: "you", requestId: "turn-end-2", answeredAt: "2026-10-04T11:42:00Z")
        var told = session("told", status: "idle", lastActive: "2026-10-04T11:43:00Z")
        told.endTurn = SessionEndTurn(headline: "Ready", needs: "you", askedSession: "lead")
        let asks = SessionAttention.build(sessions: [asking, answered, told], attention: [], deliveryProblems: [])
        expect(asks["asking"] == SessionAttention(kind: .request, reason: "Asks: Ready to ship"), "an ended turn's ask needs the person: \(String(describing: asks["asking"]))")
        expect(asks["answered"] == nil && asks["told"] == nil, "an answered ask, or one told to an agent, does not")
        print("session attention tests passed")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
