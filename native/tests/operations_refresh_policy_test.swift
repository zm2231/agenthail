import Foundation

@main
struct OperationsRefreshPolicyTest {
    static func main() {
        guard OperationsRefreshPolicy.reloadOperations(for: "device.paired") else { exit(1) }
        guard OperationsRefreshPolicy.reloadOperations(for: "device.push.updated") else { exit(1) }
        guard OperationsRefreshPolicy.reloadOperations(for: "settings.updated") else { exit(1) }
        guard !OperationsRefreshPolicy.reloadOperations(for: "turn.completed") else { exit(1) }
        guard OperationsRefreshPolicy.reloadAudit(for: "state.changed") else { exit(1) }
        guard OperationsRefreshPolicy.reloadAudit(for: "turn.completed") else { exit(1) }
        guard OperationsRefreshPolicy.reloadAudit(for: "session.updated") else { exit(1) }
        guard !OperationsRefreshPolicy.reloadAudit(for: "stream.reset") else { exit(1) }
    }
}
