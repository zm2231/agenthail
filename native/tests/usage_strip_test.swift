@testable import AgenthailHelpers
import Foundation

@main
struct UsageStripTest {
    static func main() throws {
        let now = try date("2026-10-23T14:00:00Z")
        let report = try JSONDecoder().decode(UsageReport.self, from: Data(#"""
        {"sets":[
          {"host":"studio","hostId":"h1","local":true,"provider":"claude","account":"work@example.com","root":"/a","primary":true,"meters":[
            {"provider":"claude","kind":"session","label":"Claude","percent":40,"asOf":"2026-10-23T13:59:00Z","aged":false,"until":"2026-10-23T16:00:00Z"}]},
          {"host":"studio","hostId":"h1","local":true,"provider":"codex","meters":[
            {"provider":"codex","kind":"weekly","label":"Codex week","percent":19,"asOf":"2026-10-23T13:59:00Z","aged":false,"until":"2026-10-26T16:00:00Z"}]},
          {"host":"studio","hostId":"h1","local":true,"provider":"claude","account":"team@example.com","root":"/b","meters":[
            {"provider":"claude","kind":"session","label":"Claude","percent":12,"asOf":"2026-10-23T13:59:00Z","aged":false,"until":"2026-10-23T16:00:00Z"},
            {"provider":"claude","kind":"weekly_all","label":"Claude week","percent":91,"severity":"critical","asOf":"2026-10-23T13:59:00Z","aged":false,"until":"2026-10-26T16:00:00Z"}]},
          {"host":"vps","hostId":"h2","local":false,"provider":"claude","account":"ops@example.com","root":"/c","primary":true,"meters":[
            {"provider":"claude","kind":"session","label":"Claude","percent":5,"asOf":"2026-10-23T13:59:00Z","aged":false,"until":"2026-10-23T13:30:00Z"}]},
          {"host":"old","hostId":"h3","local":false,"meters":[],"problem":"old runs an older Agenthail. Update it to see its usage."}
        ]}
        """#.utf8))
        let strip = UsageStrip.make(report.sets ?? [], now: now)
        check(strip.meters.map(\.label) == ["Claude", "Codex week"], "this host's primary account and Codex show in full: \(strip.meters.map(\.label))")
        check(strip.others.map(\.title) == ["team"], "another account shows compact by its name; a host whose meters all passed shows nothing: \(strip.others.map(\.title))")
        check(strip.others.first?.percent == 91 && strip.others.first?.critical == true, "a compact account shows its highest limit and flags it")
        check(strip.others.first?.spoken.hasPrefix("team@example.com: Claude usage 12 percent") == true, "the compact account reads out every limit: \(strip.others.first?.spoken ?? "")")
        check(strip.problem == nil, "a problem stays quiet while there are meters")

        let remote = UsageReport.Set(host: "vps", hostId: "h2", local: false, provider: "claude", account: "ops@example.com", meters: report.sets?[0].meters)
        check(UsageStrip.make([remote], now: now).others.map(\.title) == ["ops · vps"], "another host's account names the host")

        let failing = UsageReport.Set(host: "studio", hostId: "h1", local: true, provider: "claude", primary: true, meters: [], problem: "claude.ai could not be asked")
        let quiet = UsageStrip.make([failing], now: now)
        check(quiet.meters.isEmpty && quiet.problem == "claude.ai could not be asked", "with no meters the strip says why")
        check(UsageStrip.make([], now: now) == UsageStrip(), "no sets, nothing to show")
        print("ok")
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
