@testable import AgenthailHelpers
import Foundation

@main
struct UsageMeterTextTest {
    static func main() throws {
        let now = try date("2026-10-23T14:00:00Z")
        let report = try JSONDecoder().decode(UsageReport.self, from: Data(#"""
        {"sets":[{"host":"studio","hostId":"h1","local":true,"provider":"codex","meters":[
          {"provider":"codex","kind":"5h","label":"Codex 5h","percent":21,"resetsAt":"2026-10-23T15:03:00Z","asOf":"2026-10-23T13:59:00Z","aged":false,"until":"2026-10-23T15:03:00Z"},
          {"provider":"codex","kind":"weekly","label":"Codex week","percent":19.4,"severity":"warning","resetsAt":"2026-10-26T16:00:00Z","asOf":"2026-10-23T13:40:00Z","aged":false,"until":"2026-10-26T16:00:00Z"},
          {"provider":"claude","kind":"weekly_all","label":"Claude week","percent":62,"asOf":"2026-10-23T10:40:00Z","aged":true}
        ],"problem":"claude.ai could not be asked"}]}
        """#.utf8))
        let meters = report.sets?.first?.meters ?? []
        check(meters.map { UsageMeterText.text($0, now: now) } == ["21% · resets 1h 3m", "19% · 3d 2h", "62% · as of 3h ago"], "reset countdowns, and an aged reading says how old it is")
        check(UsageMeterText.spokenLabel(meters[1], now: now) == "Codex week usage 19 percent, 3d 2h", "VoiceOver: \(UsageMeterText.spokenLabel(meters[1], now: now))")
        check(UsageMeterText.visible(meters, now: now).count == 3, "every meter shows before its until")
        check(UsageMeterText.visible(meters, now: try date("2026-10-23T15:03:00Z")).map(\.label) == ["Codex week", "Claude week"], "a meter hides once its until passes; one with no until stays")
        check(report.sets?.first?.problem == "claude.ai could not be asked" && meters[1].severity == "warning" && meters[0].severity == nil, "problems and severity decode")
        check(try JSONDecoder().decode(UsageReport.self, from: Data("{}".utf8)).sets == nil, "an empty report decodes")
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
