@testable import AgenthailHelpers
import Foundation

@main
struct ProxyPreferTest {
    static func main() throws {
        let now = try date("2026-10-26T12:00:00Z")
        let report = try JSONDecoder().decode(UsageReport.self, from: Data(#"""
        {"sets":[
          {"host":"server","hostId":"h2","provider":"proxy","name":"shared","proxy":{"name":"shared","id":"http://proxy.example:8317"},"meters":[],"accounts":[
            {"id":"claude-ops.json","provider":"claude","label":"ops@example.com","status":"active","preferred":true,"setOn":"server","setAt":"2026-10-26T11:20:00Z"},
            {"id":"claude-spare.json","provider":"claude","label":"spare@example.com","status":"active"}]}
        ]}
        """#.utf8))
        let accounts = UsageAccounts.make(report.sets ?? [], now: now)
        let ops = accounts[0]
        check(ops.proxyAccount == UsageProxyAccountRef(proxyId: "http://proxy.example:8317", id: "claude-ops.json", preferred: true, setOn: "server", setAt: try date("2026-10-26T11:20:00Z")), "a proxy account carries what a preference change needs: \(String(describing: ops.proxyAccount))")
        check(ops.subtitle(now: now)?.hasPrefix("preferred") == true, "a preferred account says so: \(ops.subtitle(now: now) ?? "")")
        check(ops.proxyAccount?.setNote?.hasPrefix("set on server at ") == true, "a value Agenthail wrote is credited")
        check(accounts[1].proxyAccount?.preferred == false && accounts[1].proxyAccount?.setNote == nil, "an account Agenthail did not change has no credit")

        let plan = try JSONDecoder().decode(ProxyPreferPlan.self, from: Data(#"""
        {"set":"only","now":true,"planId":"p1","confirm":"Prefer only spare@example.com (turns off ops@example.com)",
         "changes":[{"id":"claude-spare.json","label":"spare@example.com","before":0,"after":10},{"id":"claude-ops.json","label":"ops@example.com","before":10,"after":0}],
         "release":[{"id":"claude-ops.json","label":"ops@example.com"}],
         "hosts":[{"host":"laptop","hostId":"h1","roots":["/home/dev/.work/.claude"],"sessions":[{"id":"s6","name":"migrator"}]},{"host":"server","hostId":"h2","roots":["/srv/claude"],"sessions":[]}],
         "warnings":["The proxy does not say which account each session is on."]}
        """#.utf8))
        check(plan.lines == [
            "spare@example.com: preference 0 → 10",
            "ops@example.com: preference 10 → 0",
            "Releases the sessions on ops@example.com.",
            "laptop: /home/dev/.work/.claude · running: migrator",
            "server: /srv/claude · no running sessions",
            "The proxy does not say which account each session is on.",
        ], "the plan names every change and what it reaches, as the web does: \(plan.lines)")
        let toggle = try JSONDecoder().decode(ProxyPreferPlan.self, from: Data(#"""
        {"set":"on","planId":"p2","changes":[{"id":"claude-spare.json","label":"spare@example.com","before":0,"after":10}],"release":[],"hosts":[]}
        """#.utf8))
        check(toggle.lines == ["spare@example.com: preference 0 → 10", "Other accounts are not changed."], "a toggle says it leaves the others: \(toggle.lines)")

        let readers = UsageProxies.readers([
            [UsageReport.Set(host: "laptop", hostId: "h1", provider: "proxy", name: "shared", proxy: UsageReport.Proxy(name: "shared", id: "http://proxy.example:8317"), proxyProblem: "key_missing")],
            report.sets ?? [],
        ])
        check(readers == ["http://proxy.example:8317": 1], "a change goes through the computer that could read the proxy, not one without a key: \(readers)")
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
