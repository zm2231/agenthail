@testable import AgenthailHelpers
import Foundation

@main
struct SessionFactsTest {
    static func main() {
        let caps = Capabilities()
        func session(surface: String = "claude", status: String = "idle", queued: Int = 0, reason: String? = nil) -> SessionState {
            SessionState(id: "s1", surface: surface, name: "builder", alias: nil, status: status, lastActive: nil, queueCount: queued, open: true, current: reason != nil, currentReason: reason, capabilities: caps, readOnly: nil, readOnlyReason: nil)
        }
        func item(_ kind: String, role: String? = nil, title: String = "", timestamp: String? = nil, callId: String? = nil, status: String? = nil, truncated: Bool = false, reason: String? = nil, bodyRef: String? = nil) -> TimelineItem {
            TimelineItem(id: UUID().uuidString, kind: kind, role: role, title: title, text: "synthetic", timestamp: timestamp, callId: callId, status: status, truncated: truncated, truncationReason: reason, bodyRef: bodyRef)
        }

        expect(SessionFacts.presenceLabel(session(reason: "queued")) == "Queued", "current reason wins")
        expect(SessionFacts.presenceLabel(session(status: "busy")) == "Working", "status label without a reason")
        expect(SessionFacts.presenceLabel(session(status: "notLoaded")) == "Unavailable", "not loaded reads as unavailable")
        var waiting = session(status: "busy", reason: "working")
        waiting.waitingOn = WaitingState(kind: "question", id: "toolu_synthetic")
        expect(SessionFacts.presenceLabel(waiting) == "Waiting on you", "a turn waiting on its user says so over working")
        expect(SessionFacts.rowDetail(waiting) == "Claude · Waiting on you", "row detail names the wait")
        expect(SessionFacts.rowDetail(session(reason: "open")) == "Claude · Open", "row detail names surface and presence")
        expect(SessionFacts.rowDetail(session(surface: "codex", status: "busy", queued: 2, reason: "working")) == "Codex · Working · 2 queued", "row detail adds the queued count")
        var remote = session(reason: "open")
        remote.host = SessionHost(id: "h0123456789ab", name: "forge", state: "connected")
        expect(SessionFacts.rowDetail(remote) == "Claude · Open on forge", "row detail names a paired host")
        remote.unavailableReason = "host forge is unreachable"
        expect(SessionFacts.rowDetail(remote) == "Claude · Unavailable on forge", "row detail marks an unreachable host's session")
        var local = session(reason: "open")
        local.host = SessionHost(id: "h00000000000a", name: "mac", state: "local")
        expect(SessionFacts.rowDetail(local) == "Claude · Open", "a local session names no host")

        expect(SessionFacts.headerFacts(session(queued: 3), age: "5m") == ["3 queued", "last active 5m ago"], "idle header shows queue and last active")
        expect(SessionFacts.headerFacts(session(status: "busy"), age: "5m") == ["0 queued"], "busy header omits last active")
        expect(SessionFacts.lastActive("now") == "last active just now" && SessionFacts.lastActive("") == "last active recently", "last active wording")

        let exchanges = [item("message", role: "user"), item("message", role: "assistant"), item("toolCall"), item("text"), item("message", role: "user"), item("message", role: "user"), item("message", role: "assistant")]
        expect(SessionFacts.exchangeCount(exchanges) == 4, "exchanges rebuild like the web: a reply after a reply starts a new exchange")
        expect(SessionFacts.exchangeCount([item("message", role: "assistant")]) == 1, "a lone reply is one exchange")
        expect(SessionFacts.exchangeLabel(1) == "1 recent exchange" && SessionFacts.exchangeLabel(0) == "0 recent exchanges", "exchange label pluralizes")

        expect(SessionFacts.filterSummary(.recent, codexRecentHours: 6).contains("last 6 hours"), "recent summary names the Codex window")
        expect(SettingsCounts.history(50) == "50+" && SettingsCounts.history(12) == "12", "history count caps like the web")

        let detailed = item("toolResult", title: "Bash", timestamp: "2026-10-18T06:00:00Z", callId: "call_synthetic", status: "completed", truncated: true, reason: "size limit", bodyRef: "body/synthetic")
        expect(TimelineMetadata.fields(detailed) == ["Title: Bash", "Call: call_synthetic", "Status: completed", "2026-10-18T06:00:00Z", "Content truncated (size limit)", "Body reference: body/synthetic"], "timeline metadata lists every field like the web")
        expect(TimelineMetadata.fields(item("toolResult", truncated: true)) == ["Content truncated"], "truncation without a reason")
        expect(TimelineMetadata.fields(item("reasoning")).isEmpty, "no fields, no line")

        expect(AgenthailAPIError.userMessage(URLError(.cannotConnectToHost)) == AgenthailAPIError.disconnectedGuidance, "connection failures become reconnect guidance")
        expect(AgenthailAPIError.userMessage(AgenthailAPIError.request(409, "synthetic conflict")) == "synthetic conflict", "other errors keep their message")
        let unknown = AgenthailAPIError.outcomeUnknown(AgenthailAPIError.userMessage(URLError(.networkConnectionLost)))
        expect((unknown.errorDescription ?? "").contains(AgenthailAPIError.disconnectedGuidance) && (unknown.errorDescription ?? "").contains("outcome is unknown"), "an uncertain action keeps reconnect guidance and unknown-outcome safety")

        let sourced = [item("message", role: "user", timestamp: "t1"), item("message", role: "assistant", timestamp: "t2"), item("event", title: "Compacted", status: "done"), item("notice", timestamp: "t3"), item("attachment", timestamp: "t4")]
        let blocks = TranscriptBlock.build(sourced)
        expect(blocks.count == sourced.count && zip(blocks, sourced).allSatisfy { $0.source == $1 }, "every non-tool block keeps its timeline item for details")
        print("session facts tests passed")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
