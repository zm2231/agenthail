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
        var background = session(reason: "open")
        background.backgroundTasks = BackgroundTasks(running: true, tasks: [BackgroundTask(id: "b1", kind: "shell", description: "go test ./..."), BackgroundTask(id: "m1", kind: "monitor", description: "file watcher", ambient: true)], source: "transcript")
        expect(background.isWaitingOnBackground && SessionFacts.presenceLabel(background) == "Waiting on background work", "a session between turns with background work says so")
        expect(background.backgroundTasks?.summary == "1 background task running", "ambient tasks are not counted")
        expect(background.backgroundTasks?.tasks?.last?.line == "Monitor: file watcher (ambient)", "ambient tasks are listed and marked")
        expect(background.backgroundTasks?.sourceNote == "Read from the transcript.", "the list says where it came from")
        let backgroundTurn = SessionState(id: "s1", surface: "claude", name: "builder", alias: nil, status: "busy", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: "working", capabilities: caps, readOnly: nil, readOnlyReason: nil, backgroundTasks: background.backgroundTasks)
        expect(!backgroundTurn.isWaitingOnBackground && SessionFacts.presenceLabel(backgroundTurn) == "Working", "a turn in progress reads as working")
        var ambientOnly = session(reason: "open")
        ambientOnly.backgroundTasks = BackgroundTasks(running: false, tasks: [BackgroundTask(id: "m1", kind: "monitor", ambient: true)], source: "host")
        expect(!ambientOnly.isWaitingOnBackground && SessionFacts.presenceLabel(ambientOnly) == "Open", "ambient tasks alone do not hold the session")
        expect(SessionFacts.rowDetail(session(surface: "codex", status: "busy", queued: 2, reason: "working")) == "Codex · Working · 2 queued", "row detail adds the queued count")
        var remote = session(reason: "open")
        remote.host = SessionHost(id: "h0123456789ab", name: "vps", state: "connected")
        expect(SessionFacts.rowDetail(remote) == "Claude · Open on vps", "row detail names a paired host")
        remote.unavailableReason = "host vps is unreachable"
        expect(SessionFacts.rowDetail(remote) == "Claude · Unavailable on vps", "row detail marks an unreachable host's session")
        var local = session(reason: "open")
        local.host = SessionHost(id: "h00000000000a", name: "laptop", state: "local")
        expect(SessionFacts.rowDetail(local) == "Claude · Open", "a local session names no host")

        expect(SessionFacts.headerFacts(session(queued: 3), age: "5m") == ["3 queued", "last active 5m ago"], "idle header shows queue and last active")
        expect(SessionFacts.headerFacts(session(status: "busy"), age: "5m") == ["0 queued"], "busy header omits last active")
        expect(SessionFacts.lastActive("now") == "last active just now" && SessionFacts.lastActive("") == "last active recently", "last active wording")

        expect(SessionFacts.exchangeLabel(1) == "1 recent exchange" && SessionFacts.exchangeLabel(0) == "0 recent exchanges", "exchange label pluralizes")

        expect(SessionFacts.filterSummary(.recent, codexRecentHours: 6).contains("last 6 hours"), "recent summary names the Codex window")
        expect(SettingsCounts.history(50) == "50+" && SettingsCounts.history(12) == "12", "history count caps like the web")

        let detailed = item("toolResult", title: "Bash", timestamp: "2026-10-18T06:00:00Z", callId: "call_synthetic", status: "completed", truncated: true, reason: "size limit", bodyRef: "body/synthetic")
        expect(TimelineMetadata.note(detailed) == "Shortened by the host", "a shortened record says so and nothing else")
        expect(TimelineMetadata.note(item("toolResult", title: "Bash", timestamp: "2026-10-18T06:00:00Z", callId: "call_synthetic", status: "completed", bodyRef: "body/synthetic")) == nil, "titles, call ids, statuses, timestamps and body references are not shown")

        let shortenedRecords = [
            item("message", role: "user", truncated: true),
            item("message", role: "assistant", truncated: true),
            item("message", role: "peer", truncated: true),
            item("instructions", truncated: true),
            item("attachment", truncated: true),
            item("notice", truncated: true),
            item("event", title: "Turn duration", truncated: true),
            item("event", title: "Hook", truncated: true),
        ]
        for record in shortenedRecords {
            let blocks = TranscriptBlock.build([record])
            expect(!blocks.isEmpty && blocks.allSatisfy(\.truncated), "a shortened \(record.kind) \(record.role ?? "") record keeps its note in the transcript")
        }

        expect(AgenthailAPIError.userMessage(URLError(.cannotConnectToHost)) == AgenthailAPIError.disconnectedGuidance, "connection failures become reconnect guidance")
        expect(AgenthailAPIError.userMessage(AgenthailAPIError.request(409, "synthetic conflict")) == "synthetic conflict", "other errors keep their message")
        let unknown = AgenthailAPIError.outcomeUnknown(AgenthailAPIError.userMessage(URLError(.networkConnectionLost)))
        expect((unknown.errorDescription ?? "").contains(AgenthailAPIError.disconnectedGuidance) && (unknown.errorDescription ?? "").contains("outcome is unknown"), "an uncertain action keeps reconnect guidance and unknown-outcome safety")

        print("session facts tests passed")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
