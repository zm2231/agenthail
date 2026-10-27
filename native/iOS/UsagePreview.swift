#if DEBUG
import Foundation

enum UsagePreview {
    static func json(now: Date = Date()) -> String {
        let stamp = ISO8601DateFormatter()
        let asOf = stamp.string(from: now.addingTimeInterval(-90))
        func meter(_ provider: String, _ kind: String, _ label: String, _ percent: Int, hours: Double, critical: Bool = false) -> String {
            let severity = critical ? #","severity":"critical""# : ""
            return #"{"provider":"\#(provider)","kind":"\#(kind)","label":"\#(label)","percent":\#(percent)\#(severity),"resetsAt":"\#(stamp.string(from: now.addingTimeInterval(hours * 3600)))","asOf":"\#(asOf)","aged":false}"#
        }
        let primary = [meter("claude", "session", "Claude", 38, hours: 2.3), meter("claude", "weekly_all", "Claude week", 61, hours: 76)]
        let codex = [meter("codex", "weekly", "Codex week", 19, hours: 120)]
        let team = [meter("claude", "session", "Claude", 22, hours: 1.4), meter("claude", "weekly_all", "Claude week", 92, hours: 30, critical: true), meter("claude", "weekly_scoped", "Claude week, Fable", 40, hours: 30).replacingOccurrences(of: #""percent""#, with: #""scope":"Fable","percent""#)]
        let noLogin = #""login":"api_key","problem":"Signed in with an API key; no usage reported.","problemDetail":"claude.ai reports usage only for a claude.ai login, and this config root uses an API key.""#
        let shared = #""proxy":{"name":"shared","id":"http://proxy.example:8317","url":"http://proxy.example:8317"}"#
        let ops = [meter("claude", "session", "Claude", 18, hours: 3.1), meter("claude", "weekly", "Claude week", 41, hours: 52)].map { $0.replacingOccurrences(of: #""aged""#, with: #""ceiling":60,"aged""#) }
        let lead = [meter("claude", "session", "Claude", 64, hours: 1.2, critical: true), meter("claude", "weekly", "Claude week", 33, hours: 52)].map { $0.replacingOccurrences(of: #""aged""#, with: #""ceiling":60,"aged""#) }
        let retry = stamp.string(from: now.addingTimeInterval(80 * 60))
        let proxy = #"{"host":"build-box","hostId":"h0123456789ab","local":false,"provider":"claude","name":"work","login":"proxy","root":"/home/dev/.work/.claude",\#(shared),"meters":[]},{"host":"build-box","hostId":"h0123456789ab","local":false,"provider":"proxy","name":"shared",\#(shared),"meters":[],"accounts":[{"id":"claude-ops.json","provider":"claude","label":"ops@example.com","status":"active","preferred":true,"setOn":"build-box","setAt":"\#(stamp.string(from: now.addingTimeInterval(-40 * 60)))","asOf":"\#(asOf)","meters":[\#(ops.joined(separator: ","))]},{"id":"claude-lead.json","provider":"claude","label":"lead@example.com","status":"active","asOf":"\#(asOf)","meters":[\#(lead.joined(separator: ","))]},{"id":"claude-spare.json","provider":"claude","label":"spare@example.com","status":"cooling","nextRetry":"\#(retry)","meters":[]}]}"#
        return #"{"sets":[{"host":"computer","hostId":"computer","local":true,"provider":"claude","name":"default","account":"demo@example.com","root":"/Users/demo/.claude","primary":true,"asOf":"\#(asOf)","meters":[\#(primary.joined(separator: ","))]},{"host":"computer","hostId":"computer","local":true,"provider":"codex","meters":[\#(codex.joined(separator: ","))]},{"host":"computer","hostId":"computer","local":true,"provider":"claude","name":"team","account":"team@example.com","root":"/Users/demo/.claude-team","asOf":"\#(asOf)","meters":[\#(team.joined(separator: ","))]},{"host":"build-box","hostId":"h0123456789ab","local":false,"provider":"claude","name":"default","root":"/home/dev/.claude","primary":true,"meters":[],\#(noLogin)},\#(proxy)]}"#
    }

    static func preferPlan(only: Bool) -> String {
        let hosts = #""hosts":[{"host":"build-box","hostId":"h0123456789ab","roots":["/home/dev/.work/.claude"],"sessions":[{"id":"remote-demo-2","name":"migrator"}]}]"#
        if only {
            return #"{"set":"only","now":false,"planId":"preview-only","confirm":"Prefer only lead@example.com (turns off ops@example.com)","changes":[{"id":"claude-lead.json","label":"lead@example.com","before":0,"after":10},{"id":"claude-ops.json","label":"ops@example.com","before":10,"after":0}],"release":[],\#(hosts)}"#
        }
        return #"{"set":"on","now":false,"planId":"preview-on","confirm":"Prefer lead@example.com","changes":[{"id":"claude-lead.json","label":"lead@example.com","before":0,"after":10}],"release":[],\#(hosts)}"#
    }
}
#endif
