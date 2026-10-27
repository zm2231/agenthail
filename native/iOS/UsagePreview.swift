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
        return #"{"sets":[{"host":"computer","hostId":"computer","local":true,"provider":"claude","name":"default","account":"demo@example.com","root":"/Users/demo/.claude","primary":true,"asOf":"\#(asOf)","meters":[\#(primary.joined(separator: ","))]},{"host":"computer","hostId":"computer","local":true,"provider":"codex","meters":[\#(codex.joined(separator: ","))]},{"host":"computer","hostId":"computer","local":true,"provider":"claude","name":"team","account":"team@example.com","root":"/Users/demo/.claude-team","asOf":"\#(asOf)","meters":[\#(team.joined(separator: ","))]},{"host":"build-box","hostId":"h0123456789ab","local":false,"provider":"claude","name":"default","root":"/home/dev/.claude","primary":true,"meters":[],\#(noLogin)}]}"#
    }
}
#endif
