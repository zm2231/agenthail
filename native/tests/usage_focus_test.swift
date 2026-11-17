@testable import AgenthailHelpers
import Foundation

@main
struct UsageFocusTest {
    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }

    static func main() throws {
        let now = ISO8601DateFormatter().date(from: "2026-10-23T14:00:00Z")!
        func meter(_ percent: Double, critical: Bool = false) -> UsageReport.Meter {
            UsageReport.Meter(provider: "claude", kind: "weekly_all", label: "Claude week", percent: percent, severity: critical ? "critical" : nil, asOf: "2026-10-23T13:59:00Z", aged: false)
        }
        var work = UsageReport.Set(host: "studio", hostId: "h1", provider: "claude", name: "default", account: "work@example.com", root: "/a")
        work.meters = [meter(40)]
        var team = UsageReport.Set(host: "studio", hostId: "h1", provider: "claude", name: "team", account: "team@example.com", root: "/b")
        team.meters = [meter(91, critical: true)]
        var codex = UsageReport.Set(host: "studio", hostId: "h1", provider: "codex")
        codex.meters = [UsageReport.Meter(provider: "codex", kind: "weekly", label: "Codex week", percent: 19, asOf: "2026-10-23T13:59:00Z", aged: false)]
        let accounts = UsageAccounts.make([work, team, codex], now: now)
        let host = SessionHost(id: "h1", name: "studio", state: "local")
        func session(_ id: String, surface: String, root: String?, busy: Bool, current: Bool = false) -> SessionState {
            var row = SessionState(id: id, surface: surface, name: id, alias: nil, status: busy ? "busy" : "idle", lastActive: nil, queueCount: 0, open: true, current: current, currentReason: nil, capabilities: Capabilities(), readOnly: nil, readOnlyReason: nil)
            row.host = host
            row.claudeConfigRoot = root.map { SessionConfigRoot(name: $0, root: $0) }
            return row
        }
        let onWork = session("w", surface: "claude", root: "/a", busy: false)
        let onTeam = session("t", surface: "claude", root: "/b", busy: false, current: true)
        let codexBusy = [session("c1", surface: "codex", root: nil, busy: true), session("c2", surface: "codex", root: nil, busy: true)]

        let shown = UsageFocus.pick(accounts, shown: onWork, sessions: [onWork, onTeam])
        check(shown.chips.map(\.title) == ["work", "team"] && shown.others == 1, "the shown session's account leads, a critical account in use joins it, the rest are counted: \(shown.chips.map(\.title)) +\(shown.others)")

        let busiest = UsageFocus.pick(accounts, shown: nil, sessions: codexBusy + [session("w2", surface: "claude", root: "/a", busy: true)])
        check(busiest.chips.first?.provider == "codex" && busiest.others == 2, "with nothing shown the account with the most working sessions leads: \(busiest.chips.map(\.title))")

        let fullest = UsageFocus.pick(accounts, shown: nil, sessions: [])
        check(fullest.chips.map(\.title) == ["team"] && fullest.others == 2, "with nothing working the fullest account leads, and an idle critical one isn't repeated: \(fullest.chips.map(\.title))")

        check(UsageFocus.pick([], shown: onWork, sessions: [onWork]) == UsageFocus(chips: [], others: 0), "no accounts shows nothing")
        check(UsageFocus.name(accounts[0]) == "Claude work", "a Claude account is named for its provider and login: \(UsageFocus.name(accounts[0]))")
        check(UsageFocus.line(accounts[1], now: now).hasPrefix("Claude team: ") && UsageFocus.line(accounts[1], now: now).contains("91"), "the tooltip line names the account and its meters: \(UsageFocus.line(accounts[1], now: now))")
        print("usage focus ok")
    }
}
