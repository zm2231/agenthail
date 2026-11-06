@testable import AgenthailHelpers
import Foundation

@main
struct SnapshotRefreshPolicyTest {
    static func check(_ condition: Bool, _ message: String) {
        guard !condition else { return }
        print("FAIL: \(message)")
        exit(1)
    }

    static func main() {
        for type in ["stream.reset", "state.changed", "settings.updated", "device.push.updated", "device.revoked", "host.state", "host.paired", "host.removed"] {
            check(SnapshotRefreshPolicy.refreshes(for: type), "\(type) changes what the snapshot carries")
        }
        for type in ["session.updated", "turn.completed", "ledger.gate", "ledger.check.started", "ledger.question.answered", "task.settled", "signin.changed", "ui.changed"] {
            check(!SnapshotRefreshPolicy.refreshes(for: type), "\(type) arrives through its own stream or not at all")
        }
        var due = EventRefresh(for: "device.paired", auditing: false)
        due.merge(EventRefresh(for: "ledger.gate", auditing: false))
        check(due.snapshot && due.operations && !due.audit, "merged needs keep the earlier event's: \(due)")
        check(EventRefresh(for: "ledger.gate", auditing: false).isEmpty, "a ledger event with Operations closed costs nothing")
        check(EventRefresh(for: "ledger.gate", auditing: true).audit, "an open audit log still follows every event")
        print("ok   snapshot_refresh_policy_test")
    }
}
