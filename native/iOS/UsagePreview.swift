#if DEBUG
import Foundation

enum UsagePreview {
    static func json(now: Date = Date()) -> String {
        let stamp = ISO8601DateFormatter()
        func meter(_ provider: String, _ kind: String, _ label: String, _ percent: Int, hours: Double, critical: Bool = false) -> String {
            let severity = critical ? #","severity":"critical""# : ""
            return #"{"provider":"\#(provider)","kind":"\#(kind)","label":"\#(label)","percent":\#(percent)\#(severity),"resetsAt":"\#(stamp.string(from: now.addingTimeInterval(hours * 3600)))","asOf":"\#(stamp.string(from: now))","aged":false}"#
        }
        let primary = [meter("claude", "session", "Claude", 38, hours: 2.3), meter("claude", "week", "Claude week", 61, hours: 76)]
        let codex = [meter("codex", "week", "Codex week", 19, hours: 120)]
        let team = [meter("claude", "week", "Claude week", 92, hours: 30, critical: true)]
        let build = [meter("claude", "session", "Claude", 14, hours: 4)]
        return #"{"sets":[{"host":"computer","hostId":"computer","local":true,"provider":"claude","account":"demo@example.com","root":"/Users/demo/.claude","primary":true,"meters":[\#(primary.joined(separator: ","))]},{"host":"computer","hostId":"computer","local":true,"provider":"codex","meters":[\#(codex.joined(separator: ","))]},{"host":"computer","hostId":"computer","local":true,"provider":"claude","account":"team@example.com","root":"/Users/demo/.claude-team","meters":[\#(team.joined(separator: ","))]},{"host":"build-box","hostId":"h0123456789ab","local":false,"provider":"claude","account":"ops@example.com","root":"/home/ops/.claude","primary":true,"meters":[\#(build.joined(separator: ","))]}]}"#
    }
}
#endif
