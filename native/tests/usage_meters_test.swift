@testable import AgenthailHelpers
import Foundation

@main
struct UsageMetersTest {
    static func main() throws {
        let now = try date("2026-10-23T14:00:00Z")
        let codex = try usage(#"{"fetchedAt":"2026-10-23T13:40:00Z","limits":[{"kind":"weekly","percent":19.4,"resetsAt":"2026-10-26T16:00:00Z"}]}"#)
        let live = UsageMeters.meters(provider: "Codex", usage: codex, now: now)
        check(live == [UsageMeter(label: "Codex week", percent: 19.4, detail: "3d 2h")], "a fresh Codex reading shows its weekly window and reset: \(live)")
        check(live[0].text == "19% · 3d 2h" && live[0].spokenLabel == "Codex week usage 19 percent, 3d 2h", "text and VoiceOver: \(live[0].text) / \(live[0].spokenLabel)")

        let aged = UsageMeters.meters(provider: "Codex", usage: codex, now: try date("2026-10-23T16:45:00Z"))
        check(aged.map(\.detail) == ["as of 3h ago"], "a reading over an hour old says how old it is instead of looking live: \(aged)")
        check(UsageMeters.meters(provider: "Codex", usage: codex, now: try date("2026-10-26T16:00:00Z")).isEmpty, "a window whose reset has passed shows nothing")
        check(UsageMeters.meters(provider: "Codex", usage: codex, now: try date("2026-10-31T13:41:00Z")).isEmpty, "a reading older than its window shows nothing")

        let both = try usage(#"{"fetchedAt":"2026-10-23T13:59:00Z","limits":[{"kind":"5h","percent":21,"resetsAt":"2026-10-23T15:03:00Z"},{"kind":"weekly","percent":67,"resetsAt":"2026-10-25T13:00:00Z"}]}"#)
        check(UsageMeters.meters(provider: "Codex", usage: both, now: now).map(\.text) == ["21% · resets 1h 3m", "67% · 1d 23h"], "two windows read short first")

        let claude = try usage(#"{"fetchedAt":"2026-10-23T13:59:30Z","limits":[{"kind":"session","percent":42,"resetsAt":"2026-10-23T15:03:00Z"},{"kind":"weekly_all","percent":30},{"kind":"weekly_scoped","percent":0}]}"#)
        check(UsageMeters.meters(provider: "Claude", usage: claude, now: now).map(\.label) == ["Claude", "Claude week"], "Claude keeps its session label, names its week, and drops an empty model limit")
        check(UsageMeters.meters(provider: "Codex", usage: nil, now: now).isEmpty, "no reading shows no meter")
        print("ok")
    }

    static func usage(_ json: String) throws -> ProviderUsage {
        try JSONDecoder().decode(ProviderUsage.self, from: Data(json.utf8))
    }

    static func date(_ raw: String) throws -> Date {
        guard let date = UsageMeters.date(raw) else { throw CocoaError(.coderReadCorrupt) }
        return date
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
