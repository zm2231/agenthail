@testable import AgenthailHelpers
import Foundation

@main
struct UsageProxiesTest {
    static func main() throws {
        let now = try date("2026-10-26T12:00:00Z")
        let report = try JSONDecoder().decode(UsageReport.self, from: Data(#"""
        {"sets":[
          {"host":"laptop","hostId":"h1","local":true,"provider":"claude","name":"default","account":"dev@example.com","root":"/a","primary":true,"meters":[]},
          {"host":"laptop","hostId":"h1","local":true,"provider":"claude","name":"work","login":"proxy","root":"/w","account":"stale@example.com","proxy":{"name":"shared","id":"http://proxy.example:8317"},"meters":[]},
          {"host":"laptop","hostId":"h1","local":true,"provider":"proxy","name":"shared","proxy":{"name":"shared","id":"http://proxy.example:8317"},"meters":[],"proxyProblem":"key_missing","problem":"No management key for the shared proxy on this host."},
          {"host":"laptop","hostId":"h1","local":true,"provider":"proxy","name":"local","proxy":{"name":"local","id":"http://loopback:8317@h1"},"meters":[],"proxyProblem":"unreachable","problem":"local proxy unreachable"}
        ]}
        """#.utf8))
        let server = try JSONDecoder().decode(UsageReport.self, from: Data(#"""
        {"sets":[
          {"host":"server","hostId":"h2","provider":"claude","name":"default","login":"proxy","root":"/srv","proxy":{"name":"shared","id":"http://proxy.example:8317"},"meters":[]},
          {"host":"server","hostId":"h2","provider":"proxy","name":"shared","proxy":{"name":"shared","id":"http://proxy.example:8317"},"meters":[],
           "problem":"Every shared Claude account is over its ceiling; the first frees up in 2h 20m.","accounts":[
            {"id":"a","provider":"claude","label":"dev@example.com","status":"active","asOf":"2026-10-26T11:48:00Z","meters":[
              {"provider":"claude","kind":"session","label":"Claude","percent":18,"ceiling":60,"asOf":"2026-10-26T11:48:00Z","aged":false,"resetsAt":"2026-10-26T15:00:00Z"},
              {"provider":"claude","kind":"weekly","label":"Claude week","percent":61,"ceiling":60,"severity":"critical","asOf":"2026-10-26T11:48:00Z","aged":false}]},
            {"id":"b","provider":"claude","label":"team@example.com","status":"cooling","nextRetry":"2026-10-26T14:20:00Z"},
            {"id":"c","provider":"codex","label":"dev@example.com","status":"active"}]}
        ]}
        """#.utf8))
        let merged = UsageAccounts.merged([report.sets, server.sets])
        check(merged.filter { $0.provider == "proxy" }.count == 2, "a proxy two computers report shows once; two proxies on one host both show: \(merged.map { $0.name ?? "" })")
        check(merged.first { $0.proxy?.id == "http://proxy.example:8317" && $0.provider == "proxy" }?.host == "server", "the copy read with a working key wins")

        let accounts = UsageAccounts.make(merged, now: now)
        check(accounts.map(\.title) == ["dev", "dev · shared", "team", "dev (Codex)", "local (proxy)"], "proxy accounts are named like others, told apart by proxy: \(accounts.map(\.title))")
        check(!accounts.contains { $0.root == "/w" || $0.root == "/srv" }, "a root through a proxy is part of its group, not an account")
        let dev = accounts[1]
        check(dev.critical && dev.fullTitle == "dev@example.com via the shared proxy", "an account at its proxy ceiling is critical: \(dev.fullTitle)")
        check(accounts[2].problem == "cooling for 2h 20m" && accounts[2].problemTone([]) == .quiet, "a cooling account says for how long")
        check(accounts[4].problemTone([]) == .warning, "a proxy that cannot be read warns")
        check(dev.subtitle(now: now) == "cap 60% · over cap · read 12m ago" && dev.rowLabel(dev.meters[1]) == "Week", "one proxy ceiling shows once, and says when a window is over it: \(dev.subtitle(now: now) ?? "")")

        let strip = UsageProxies.strip(accounts)
        check(strip.map(\.title) == ["dev", "shared · dev", "local (proxy)"], "each proxy is one chip named for it and its fullest account: \(strip.map(\.title))")
        check(strip[1].meters.map(\.percent) == [61] && strip[1].critical, "the proxy chip carries its worst window, in its tone")
        check(strip[2].meters.isEmpty && strip[2].problemTone([]) == .warning, "an unreadable proxy's chip still warns")
        let unread = UsageProxies.strip(accounts.filter { $0.group?.name == "shared" && $0.login == "team@example.com" })
        check(unread.map(\.title) == ["shared · team"] && unread[0].meters.isEmpty, "a readable proxy with no reading yet still names its account: \(unread.map(\.title))")

        let groups = UsageProxies.byProxy(accounts)
        check(groups.map(\.group.title) == ["shared (proxy)", "local (proxy)"] && groups[0].accounts.count == 3 && groups[0].readable && !groups[1].readable, "one group per proxy")
        check(groups[0].group.roots.count == 2 && groups[0].group.problem?.hasPrefix("Every shared") == true, "the group knows every root through it on every host")
        let onLaptop = session(id: "s1", host: "h1", root: "/w"), onServer = session(id: "s2", host: "h2", root: "/srv"), direct = session(id: "s3", host: "h1", root: "/a")
        check(dev.activeSessions([onLaptop, onServer, direct]).map(\.id) == ["s1", "s2"], "a proxy's sessions are those on its roots on any host")

        let roots = [
            ClaudeConfigRootOption(name: "default", root: "/a", primary: true, signedInAs: .init(email: "dev@example.com")),
            ClaudeConfigRootOption(name: "work", root: "/w", primary: false, signedInAs: .init(email: "stale@example.com"), login: "proxy", proxy: "shared"),
        ]
        check(ClaudeRootLabels.titles(roots, host: "laptop") == ["dev", "shared (proxy)"] && roots[1].selectable, "a root through a proxy is named for it and can be chosen")
        print("ok")
    }

    static func session(id: String, host: String, root: String) -> SessionState {
        var state = SessionState(id: id, surface: "claude", name: id, alias: nil, status: "busy", lastActive: nil, queueCount: 0, open: true, current: false, currentReason: nil, capabilities: Capabilities(), readOnly: nil, readOnlyReason: nil)
        state.host = SessionHost(id: host, name: host, state: "connected")
        state.claudeConfigRoot = SessionConfigRoot(name: root, root: root)
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
