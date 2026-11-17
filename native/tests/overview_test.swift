@testable import AgenthailHelpers
import Foundation

@main
struct OverviewTest {
    static func main() {
        let caps = Capabilities()
        func surface(_ json: String) -> SurfaceState {
            try! JSONDecoder().decode(SurfaceState.self, from: Data(json.utf8))
        }
        func session(_ id: String, surface: String, status: String = "idle", open: Bool = false, reason: String? = nil, queued: Int = 0) -> SessionState {
            SessionState(id: id, surface: surface, name: id, alias: nil, status: status, lastActive: nil, queueCount: queued, open: open, current: reason != nil, currentReason: reason, capabilities: caps, readOnly: nil, readOnlyReason: nil)
        }
        let codex = surface(#"{"name":"codex","connected":false,"health":"degraded","healthDetail":"Codex app server is reachable but not supervised","repairAction":"runtime-ensure","repairLabel":"Start managed runtime","capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":true,"interrupt":true,"steer":true},"runtime":{"name":"Codex app server","reachable":true,"durable":false,"problem":"stopped","detail":"not supervised","remediation":"start the managed runtime"}}"#)
        let claude = surface(#"{"name":"claude","connected":true,"health":"healthy","capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":true,"interrupt":true,"steer":true}}"#)
        let notion = surface(#"{"name":"notion","connected":false,"health":"unavailable","error":"notion returned 401","capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":true,"interrupt":true,"steer":true}}"#)
        let sessions = [
            session("c1", surface: "codex", status: "busy", reason: "working", queued: 2),
            session("c2", surface: "codex", reason: "recent", queued: 1),
            session("c3", surface: "codex"),
            session("k1", surface: "claude", open: true, reason: "open"),
            session("k2", surface: "claude", open: false)
        ]

        let cards = SurfaceOverview.build(surfaces: [codex, claude, notion], sessions: sessions, codexRecentHours: 8)
        expect(cards.map(\.name) == ["Codex", "Claude", "Notion"], "cards keep the daemon's surface order: \(cards.map(\.name))")
        let codexCard = cards[0]
        expect(codexCard.health == .degraded && codexCard.stateLabel == "Needs attention", "degraded health needs attention")
        expect(codexCard.working == 1 && codexCard.current == 1 && codexCard.queued == 3, "Codex counts working, recent and queued: \(codexCard.working) \(codexCard.current) \(codexCard.queued)")
        expect(codexCard.currentWindow == "Past 8h", "Codex presence uses the recent window")
        expect(codexCard.detail == "The Codex background service will stop after a restart.", "unsupervised detail reads plainly: \(codexCard.detail)")
        expect(codexCard.availability == "Codex app server is open now but will not restart automatically.", "a reachable runtime that is not durable says so")
        expect(codexCard.advice == ["not supervised. Fix: start the managed runtime"], "runtime advice carries the fix: \(codexCard.advice)")
        expect(codexCard.problem?.fix == .repair(action: "runtime-ensure", label: "Start Codex Service") && codexCard.problem?.details.contains("not supervised. Fix: start the managed runtime") == true, "the service problem offers its repair and keeps the advice in the details")
        expect(codexCard.surface.repairAction == "runtime-ensure" && codexCard.surface.repairLabel == "Start managed runtime", "the repair action decodes")
        let claudeCard = cards[1]
        expect(claudeCard.health == .healthy && claudeCard.stateLabel == "Healthy" && claudeCard.detail == "Ready", "a healthy surface with no detail is ready")
        expect(claudeCard.current == 1 && claudeCard.currentWindow == "Open now", "Claude presence counts open sessions")
        expect(claudeCard.availability == nil && claudeCard.surface.repairAction == nil, "no runtime means no availability line or repair")
        let notionCard = cards[2]
        expect(notionCard.health == .unavailable && notionCard.detail == "Notion is not signed in on this Mac.", "a Notion 401 reads as signed out: \(notionCard.detail)")
        let unset = surface(#"{"name":"notion","connected":false,"health":"signin_off","healthDetail":"Notion: browser sign-in is turned off; to turn it back on, run `agenthail signin use notion auto` or open Settings > Browser Sign-in","capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":true,"interrupt":true,"steer":true}}"#)
        let unsetCard = SurfaceOverview.build(surfaces: [unset], sessions: [], codexRecentHours: 8)[0]
        let unsetClaude = surface(#"{"name":"claude","connected":true,"health":"signin_off","healthDetail":"Claude Remote Control: browser sign-in is turned off; to turn it back on, run `agenthail signin use claude auto` or open Settings > Browser Sign-in","capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":true,"interrupt":true,"steer":true}}"#)
        let unsetClaudeCard = SurfaceOverview.build(surfaces: [unsetClaude], sessions: [], codexRecentHours: 8)[0]
        expect(unsetClaudeCard.detail == "Claude Remote Control can't sign in: browser sign-in is off." && unsetClaudeCard.problem?.fix == .browserSignIn, "Claude with sign-in off says so in one sentence and offers Browser Sign-in: \(unsetClaudeCard.detail)")
        expect(unsetCard.health == .signinOff && unsetCard.stateLabel == "Sign-in off" && unsetCard.detail == "Notion can't sign in: browser sign-in is off.", "sign-in turned off is not unavailable: \(unsetCard.detail)")
        expect(unsetCard.problem?.details.first?.contains("agenthail signin use notion auto") == true, "the command stays in the details: \(unsetCard.problem?.details ?? [])")
        let headless = surface(#"{"name":"notion","connected":false,"health":"signin_unavailable","healthDetail":"Notion: No browser on this computer, so Notion sign-in is unavailable here. If a browser is installed, pin one of its profiles with `agenthail signin use notion <profile>` (see `agenthail signin list`).","capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":true,"interrupt":true,"steer":true}}"#)
        let headlessCard = SurfaceOverview.build(surfaces: [headless], sessions: [], codexRecentHours: 8)[0]
        expect(headlessCard.health == .signinUnavailable && headlessCard.stateLabel == "No browser here" && headlessCard.detail == "Notion can't sign in: this computer has no browser.", "a computer with no browser is a stated limit: \(headlessCard.detail)")
        let safari = surface(#"{"name":"claude","connected":true,"health":"degraded","healthDetail":"Claude Remote Control: Safari has no cookie store at /tmp/scratch/Library/Containers/com.apple.Safari/Data/Library/Cookies/Cookies.binarycookies; open Safari and sign in once; to change it, run `agenthail signin use claude <profile|auto|none>` (see `agenthail signin list`) or open Settings > Browser Sign-in","capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":true,"interrupt":true,"steer":true}}"#)
        let safariCard = SurfaceOverview.build(surfaces: [safari], sessions: [], codexRecentHours: 8)[0]
        expect(safariCard.detail == "Claude Remote Control can't sign in: Safari has no saved sign-in." && safariCard.problem?.fixLabel == "Open Browser Sign-in", "a missing cookie store reads as one sentence with its fix: \(safariCard.detail)")
        expect(safariCard.problem?.details.first?.contains("Cookies.binarycookies") == true, "the path stays in the details")
        let bridge = surface(#"{"name":"codex","connected":false,"health":"unavailable","healthDetail":"Codex session discovery is unavailable: no Codex Desktop renderer debug target at ws://127.0.0.1:1","repairAction":"codex-launch","repairLabel":"Launch Codex through Agenthail","capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":true,"interrupt":true,"steer":true},"runtime":{"name":"Codex Desktop bridge","reachable":false,"durable":false,"problem":"bridge_unavailable","detail":"no Codex Desktop renderer debug target at ws://127.0.0.1:1","remediation":"run 'agenthail launch codex'"}}"#)
        let bridgeCard = SurfaceOverview.build(surfaces: [bridge], sessions: [], codexRecentHours: 8)[0]
        expect(!bridgeCard.detail.contains("ws://") && bridgeCard.problem?.fixLabel == "Launch Codex", "a missing Codex bridge reads plainly and offers Launch Codex: \(bridgeCard.detail)")
        expect(bridgeCard.problem?.details.contains { $0.contains("ws://127.0.0.1:1") } == true, "the address stays in the details")
        expect(claudeCard.problem == nil, "a healthy surface has no problem")
        expect(headlessCard.isQuiet && unsetCard.isQuiet && !codexCard.isQuiet && claudeCard.isQuiet, "stated limits read quietly, problems do not")
        expect(!headless.needsAttention && !unset.needsAttention && codex.needsAttention, "stated limits need no attention")

        var snapshot = DashboardSnapshot(updatedAt: "2026-10-05T12:00:00Z", eventCursor: nil, hostEpoch: nil, catalogSeq: nil, daemon: DaemonState(running: true, pid: 1, stale: false, refreshError: nil), surfaces: [codex, claude, notion], sessions: sessions, totalSessions: sessions.count, queue: [], channels: [], relays: [], history: [], codexRecentHours: 8, busyDelivery: "queue")
        let line = OverviewSummary.line(snapshot, working: 1, needsYou: 2)
        expect(line == "1 agent app connected · 1 working · 2 need you · 3 messages queued", "the summary gives the window's working and need-you numbers, and queued deliveries apart: \(line)")
        snapshot.surfaces = []
        snapshot.sessions = []
        expect(OverviewSummary.line(snapshot, working: 0, needsYou: 0) == "No agent apps connected", "an empty daemon says nothing is connected")
        let stale = DashboardSnapshot(updatedAt: "", eventCursor: nil, hostEpoch: nil, catalogSeq: nil, daemon: DaemonState(running: true, pid: 1, stale: true, refreshError: "Codex timed out."), surfaces: [], sessions: [], totalSessions: 0, queue: [], channels: [], relays: [], history: [], codexRecentHours: 5, busyDelivery: nil)
        expect(OverviewSummary.line(stale, working: 0, needsYou: 0) == "Showing cached data. Codex timed out.", "stale data names the refresh error")

        let history = try! JSONDecoder().decode([HistoryState].self, from: Data(#"""
        [
          {"id":9,"createdAt":"2026-10-05 11:59:00","kind":"deliver","target":"@builder","message":"Run the suite","evidence":"reply_observed"},
          {"id":8,"createdAt":"2026-10-05 11:58:00","kind":"alias","target":"@builder"},
          {"id":7,"createdAt":"2026-10-05 11:57:00","kind":"deliver","target":"@reviewer","message":"Check the diff","error":"session is offline","evidence":"failed"},
          {"id":6,"createdAt":"2026-10-05 11:56:00","kind":"queue","evidence":"queued"},
          {"id":5,"createdAt":"2026-10-05 11:55:00","kind":"relay_fire","target":"@a","result":"forwarded","evidence":"novel_state"}
        ]
        """#.utf8))
        let outcomes = DeliveryOutcome.recent(history)
        expect(outcomes.map(\.id) == [9, 7, 6, 5], "only entries with delivery evidence are outcomes: \(outcomes.map(\.id))")
        expect(outcomes[0].label == "Reply observed" && outcomes[0].tone == .done && outcomes[0].detail == "Run the suite" && !outcomes[0].isError, "a reply is a finished outcome")
        expect(outcomes[1].label == "Failed" && outcomes[1].tone == .problem && outcomes[1].detail == "session is offline" && outcomes[1].isError, "a failure shows its error")
        expect(outcomes[2].target == "Agenthail" && outcomes[2].detail == "No details recorded" && outcomes[2].tone == .pending, "an outcome without target or detail still reads")
        expect(outcomes[3].label == "Relay Fire", "unknown evidence falls back to the event kind")
        let many = (1...12).map { HistoryState(id: Int64($0), createdAt: "", kind: "deliver", sessionId: nil, sourceSessionId: nil, target: nil, source: nil, queueId: nil, message: nil, result: nil, error: nil, evidence: "delivered") }
        expect(DeliveryOutcome.recent(many).count == DeliveryOutcome.limit, "recent outcomes are capped")

        expect(CodexRecentWindow.choices(including: 5) == [1, 3, 5, 8, 12, 24], "a preset keeps the preset list")
        expect(CodexRecentWindow.choices(including: 2) == [1, 2, 3, 5, 8, 12, 24], "a custom saved window stays selectable")
        expect(CodexRecentWindow.choices(including: 48) == [1, 3, 5, 8, 12, 24, 48], "a saved window above the presets stays selectable")
        expect(CodexRecentWindow.choices(including: 168) == [1, 3, 5, 8, 12, 24, 168], "the daemon maximum stays selectable")
        expect(CodexRecentWindow.choices(including: 169) == CodexRecentWindow.presets, "a value the daemon rejects is not offered")
        expect(CodexRecentWindow.label(1) == "1 hour" && CodexRecentWindow.label(12) == "12 hours", "window labels")
        print("overview tests passed")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
