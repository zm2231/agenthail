@testable import AgenthailHelpers
import Foundation

@main
struct SessionActivityTest {
    static func main() throws {
        let now = try date("2026-10-23T10:14:30Z")
        let quiet = try session(#"{"busySince":"2026-10-23T10:00:00Z","lastProgressAt":"2026-10-23T10:02:00Z","tool":{"name":"Bash","startedAt":"2026-10-23T10:02:00.5Z"}}"#)
        let since = try date("2026-10-23T10:02:00.5Z").formatted(date: .omitted, time: .shortened)
        check(quiet.workingLabel(now: now) == "Working · no activity for 12 min · running Bash since \(since)", "a quiet turn names how long and the tool it runs: \(quiet.workingLabel(now: now))")

        check(quiet.workingLabel(now: try date("2026-10-23T10:11:59Z")) == "Working", "under ten minutes of quiet says only Working")
        check(quiet.workingLabel(now: try date("2026-10-23T11:47:00Z")).hasPrefix("Working · no activity for 1 hr 45 min"), "an hour and more reads in hours and minutes")

        let orphan = try session(#"{"busySince":"2026-10-23T05:07:57Z","lastProgressAt":"2026-10-23T05:07:57Z","noProcess":true}"#)
        check(orphan.workingLabel(now: now) == "Working · no activity for 5 hr 6 min · no Claude process", "a turn nothing runs says so: \(orphan.workingLabel(now: now))")

        let codex = try JSONDecoder().decode(SessionState.self, from: Data(#"{"id":"t","surface":"codex","name":"reviewer","status":"busy","queueCount":0,"open":true,"current":true,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true},"activity":{"busySince":"2026-10-23T09:58:00.034Z","lastProgressAt":"2026-10-23T06:02:00.123456789-04:00","tool":{"name":"exec","startedAt":"2026-10-23T10:01:00.5Z"}}}"#.utf8))
        let codexSince = try date("2026-10-23T10:01:00.5Z").formatted(date: .omitted, time: .shortened)
        check(codex.workingLabel(now: now) == "Working · no activity for 12 min · running exec since \(codexSince)", "a quiet Codex turn reads the same way, whatever offset and precision its times carry: \(codex.workingLabel(now: now))")

        let fresh = try session(nil)
        check(fresh.workingLabel(now: now) == "Working", "a session without activity says Working")

        let idle = try JSONDecoder().decode(SessionState.self, from: Data(#"{"id":"s","surface":"claude","name":"builder","status":"idle","queueCount":0,"open":true,"current":true,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true},"activity":{"lastProgressAt":"2026-10-23T09:00:00Z"}}"#.utf8))
        check(idle.activity?.lastProgressAt == "2026-10-23T09:00:00Z" && idle.activity?.tool == nil && idle.activity?.noProcess == nil, "an activity without a tool or process flag decodes")
        print("ok")
    }

    static func session(_ activity: String?) throws -> SessionState {
        let field = activity.map { #","activity":\#($0)"# } ?? ""
        return try JSONDecoder().decode(SessionState.self, from: Data(#"{"id":"s","surface":"claude","name":"builder","status":"busy","queueCount":0,"open":true,"current":true,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true}\#(field)}"#.utf8))
    }

    static func date(_ raw: String) throws -> Date {
        guard let date = SessionTree.parseTimestamp(raw) else { throw CocoaError(.coderReadCorrupt) }
        return date
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
