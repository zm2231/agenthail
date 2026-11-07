@testable import AgenthailHelpers
import Foundation

@main
struct SnapshotRefreshPolicyTest {
    struct Classified: Decodable {
        let event: String
        var family: Bool? = nil
        let reread: Bool
    }

    static func check(_ condition: Bool, _ message: String) {
        guard !condition else { return }
        print("FAIL: \(message)")
        exit(1)
    }

    static func main() throws {
        let file = URL(fileURLWithPath: #filePath).deletingLastPathComponent().appendingPathComponent("../../internal/daemon/snapshot_events.json")
        let classified = try JSONDecoder().decode([Classified].self, from: Data(contentsOf: file))
        check(classified.count > 10, "the daemon's event classification was read: \(classified.count)")
        for entry in classified {
            let name = entry.family == true ? entry.event + "example" : entry.event
            check(SnapshotRefreshPolicy.refreshes(for: name) == entry.reread, "\(name) rereads \(SnapshotRefreshPolicy.refreshes(for: name)), the daemon classifies it \(entry.reread)")
        }
        check(!SnapshotRefreshPolicy.refreshes(for: "made.up"), "an unclassified event rereads nothing")

        var due = EventRefresh(for: "device.paired", auditing: false)
        check(!due.snapshot && due.operations, "a device event reloads Operations only: \(due)")
        due.merge(EventRefresh(for: "state.changed", auditing: false))
        check(due.snapshot && due.operations && !due.audit, "merged needs keep the earlier event's: \(due)")
        check(EventRefresh(for: "ledger.gate", auditing: false).isEmpty, "a ledger event with Operations closed costs nothing")
        check(EventRefresh(for: "ledger.gate", auditing: true).audit, "an open audit log still follows every event")
        print("ok   snapshot_refresh_policy_test")
    }
}
