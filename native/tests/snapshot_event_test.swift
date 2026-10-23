@testable import AgenthailHelpers
import Foundation

@main
struct SnapshotEventTest {
    static func main() throws {
        var failures = 0
        func check(_ condition: Bool, _ message: String) {
            if !condition {
                print("FAIL: \(message)")
                failures += 1
            }
        }
        let requested = try JSONDecoder().decode(AgenthailEvent.self, from: Data("""
        {"id":7,"type":"ui.snapshot.requested","timestamp":"2026-10-23T08:00:00.123456789Z","data":{"id":"abc","session":"s1","width":900,"height":600,"device":"Studio","rev":"r9","deadlineMs":20000}}
        """.utf8))
        check(requested.snapshot == SnapshotRequest(id: "abc", session: "s1", width: 900, height: 600, device: "Studio", rev: "r9", deadlineMs: 20_000), "a snapshot request carries its fields, got \(String(describing: requested.snapshot))")

        let bare = try JSONDecoder().decode(AgenthailEvent.self, from: Data("""
        {"id":8,"type":"ui.snapshot.requested","timestamp":"","data":{"id":"def","deadlineMs":20000}}
        """.utf8))
        check(bare.snapshot?.session == nil && bare.snapshot?.width == nil && bare.snapshot?.device == nil && bare.snapshot?.rev == nil, "a request for the window as shown has no session, size or device")

        let closed = try JSONDecoder().decode(AgenthailEvent.self, from: Data("""
        {"id":9,"type":"ui.snapshot.closed","timestamp":"","entityId":"def","data":null}
        """.utf8))
        check(closed.snapshot == nil && closed.entityId == "def", "a closed request names its id and carries no request")

        let other = try JSONDecoder().decode(AgenthailEvent.self, from: Data("""
        {"id":10,"type":"session.updated","timestamp":"","entityId":"s1","data":{"status":"working"}}
        """.utf8))
        check(other.snapshot == nil && other.entityId == "s1", "other events keep decoding and carry no snapshot request")

        if failures > 0 { exit(1) }
        print("snapshot event: requests and their close decode from the event stream")
    }
}
