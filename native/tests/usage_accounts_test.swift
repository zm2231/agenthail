@testable import AgenthailHelpers
import Foundation

@main
struct UsageAccountsTest {
    static func main() throws {
        let now = try date("2026-10-23T14:00:00Z")
        let report = try JSONDecoder().decode(UsageReport.self, from: Data(#"""
        {"sets":[
          {"host":"studio","hostId":"h1","local":true,"provider":"claude","name":"default","account":"work@example.com","root":"/a","primary":true,"asOf":"2026-10-23T13:59:00Z","meters":[
            {"provider":"claude","kind":"session","label":"Claude","percent":40,"asOf":"2026-10-23T13:59:00Z","aged":false,"until":"2026-10-23T16:00:00Z"}]},
          {"host":"studio","hostId":"h1","local":true,"provider":"codex","meters":[
            {"provider":"codex","kind":"weekly","label":"Codex week","percent":19,"asOf":"2026-10-23T13:59:00Z","aged":false,"until":"2026-10-26T16:00:00Z"}]},
          {"host":"studio","hostId":"h1","local":true,"provider":"claude","name":"team","account":"team@example.com","root":"/b","meters":[
            {"provider":"claude","kind":"session","label":"Claude","percent":12,"asOf":"2026-10-23T13:59:00Z","aged":false,"until":"2026-10-23T16:00:00Z"},
            {"provider":"claude","kind":"weekly_all","label":"Claude week","percent":91,"severity":"critical","asOf":"2026-10-23T13:59:00Z","aged":false,"until":"2026-10-26T16:00:00Z"}]},
          {"host":"vps","hostId":"h2","local":false,"provider":"claude","name":"default","login":"api_key","root":"/srv/claude","primary":true,"meters":[],"problem":"Signed in with an API key; no usage reported.","problemDetail":"claude.ai reports usage only for a claude.ai login."},
          {"host":"vps","hostId":"h2","local":false,"provider":"codex","meters":[
            {"provider":"codex","kind":"weekly","label":"Codex week","percent":5,"asOf":"2026-10-23T13:59:00Z","aged":false,"until":"2026-10-23T13:30:00Z"}]},
          {"host":"old","hostId":"h3","local":false,"meters":[],"problem":"old runs an older Agenthail. Update it to see its usage."}
        ]}
        """#.utf8))
        let accounts = UsageAccounts.make(report.sets ?? [], now: now)
        check(accounts.map(\.title) == ["work", "studio · Codex", "team", "vps (API key)", "vps · Codex", "old"], "every account gets a chip named by its login, the host only when names repeat across hosts: \(accounts.map(\.title))")
        check(accounts[3].problemDetail != nil, "the problem keeps its detail for help text")
        let twoKeys = [
            UsageReport.Set(host: "vps", hostId: "h2", provider: "claude", name: "a", login: "api_key", root: "/a"),
            UsageReport.Set(host: "vps", hostId: "h2", provider: "claude", name: "b", login: "api_key", root: "/b"),
            UsageReport.Set(host: "box", hostId: "h4", provider: "claude", name: "c", login: "signed_out", root: "/c"),
        ]
        let sameLocal = [
            UsageReport.Set(host: "studio", hostId: "h1", provider: "claude", name: "default", account: "work@company-a.example", root: "/a"),
            UsageReport.Set(host: "studio", hostId: "h1", provider: "claude", name: "second", account: "work@company-b.example", root: "/b"),
            UsageReport.Set(host: "vps", hostId: "h2", provider: "claude", name: "default", account: "work@company-c.example", root: "/c"),
        ]
        let sameLocalTitles = UsageAccounts.make(sameLocal, now: now).map(\.title)
        check(sameLocalTitles == ["studio · work · default", "studio · work · second", "vps · work"], "accounts one host would name alike are told apart by root: \(sameLocalTitles)")
        check(Swift.Set(sameLocalTitles).count == sameLocalTitles.count, "every chip title is distinct")
        check(UsageAccounts.make(twoKeys, now: now).map(\.title) == ["vps (API key) · a", "vps (API key) · b", "box (signed out)"], "two roots one host would name alike are told apart by root: \(UsageAccounts.make(twoKeys, now: now).map(\.title))")
        check(accounts[0].asOf == (try date("2026-10-23T13:59:00Z")), "an account carries when its usage was read")
        check(accounts[2].worst?.percent == 91 && accounts[2].critical, "an account shows its highest limit and flags it")
        check(accounts[2].spoken(now: now).hasPrefix("team@example.com: Claude usage 12 percent"), "the account reads out every limit: \(accounts[2].spoken(now: now))")
        check(accounts[3].meters.isEmpty && accounts[3].problem?.contains("API key") == true && accounts[3].fullTitle == "API key on vps", "an API-key root stays with its problem: \(accounts[3].fullTitle)")
        check(accounts[4].meters.isEmpty && accounts[4].problem == nil, "a reading past its window keeps the account with no meters")
        check(accounts[5].fullTitle == "old" && accounts[5].problem != nil, "a host that cannot answer is one account with its problem")
        check(UsageAccounts.make([], now: now).isEmpty, "no sets, nothing to show")
        check(accounts[3].problemTone([]) == .quiet, "an API-key root is a normal state")
        check(accounts[5].problemTone([]) == .failed, "a host that cannot answer is a failure")
        check(accounts[0].problemTone([]) == nil, "no problem, no tone")
        let signedOut = UsageAccounts.make([UsageReport.Set(host: "box", hostId: "h4", provider: "claude", name: "c", login: "signed_out", root: "/c", problem: "Not signed in; no usage reported.")], now: now)[0]
        check(signedOut.problemTone([]) == .quiet, "a signed-out root with nothing running is a normal state")
        check(signedOut.problemTone([session(id: "s9", surface: "claude", host: "h4", root: "/c", status: "busy")]) == .warning, "a signed-out root with running sessions warns")

        let work = accounts[0], team = accounts[2], codex = accounts[1]
        let mine = session(id: "s1", surface: "claude", host: "h1", root: "/a", status: "busy")
        let teams = session(id: "s2", surface: "claude", host: "h1", root: "/b", status: "idle", current: true)
        let quiet = session(id: "s3", surface: "claude", host: "h1", root: "/b", status: "idle")
        let remote = session(id: "s4", surface: "claude", host: "h2", root: "/a", status: "busy")
        let coder = session(id: "s5", surface: "codex", host: "h1", root: nil, status: "busy")
        let all = [mine, teams, quiet, remote, coder]
        check(work.activeSessions(all).map(\.id) == ["s1"], "a session belongs to the root and host it runs on")
        check(team.activeSessions(all).map(\.id) == ["s2"], "only working or current sessions are listed")
        check(codex.activeSessions(all).map(\.id) == ["s5"], "Codex sessions belong to their host's Codex account")

        let sets = report.sets ?? []
        let laptop = UsageReport.Set(host: "laptop", hostId: "h9", local: true, provider: "claude", account: "me@example.com", primary: true, meters: sets[0].meters)
        let both = UsageAccounts.merged([sets, [laptop, sets[3]]])
        check(both.count == sets.count + 1, "a host two paired computers both report appears once: \(both.map(\.host))")
        check(both.last?.host == "laptop" && both.last?.local == false, "only the first paired computer's own accounts count as this computer's")
        print("ok")
    }

    static func session(id: String, surface: String, host: String, root: String?, status: String, current: Bool = false) -> SessionState {
        var state = SessionState(id: id, surface: surface, name: id, alias: nil, status: status, lastActive: nil, queueCount: 0, open: true, current: current, currentReason: nil, capabilities: Capabilities(), readOnly: nil, readOnlyReason: nil)
        state.host = SessionHost(id: host, name: host, state: "connected")
        state.claudeConfigRoot = root.map { SessionConfigRoot(name: $0, root: $0) }
        return state
    }

    static func date(_ raw: String) throws -> Date {
        guard let date = UsageMeterText.date(raw) else { throw CocoaError(.coderReadCorrupt) }
        return date
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
