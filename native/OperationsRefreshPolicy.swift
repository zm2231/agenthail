import Foundation

enum OperationsRefreshPolicy {
    static func reloadOperations(for eventType: String) -> Bool {
        eventType.hasPrefix("device.") || eventType == "settings.updated"
    }

    static func reloadAudit(for eventType: String) -> Bool {
        eventType != "stream.reset"
    }
}

struct EventRefresh: Equatable {
    var snapshot = false
    var operations = false
    var audit = false

    init() {}

    init(for eventType: String, auditing: Bool) {
        snapshot = SnapshotRefreshPolicy.refreshes(for: eventType)
        operations = OperationsRefreshPolicy.reloadOperations(for: eventType)
        audit = auditing && OperationsRefreshPolicy.reloadAudit(for: eventType)
    }

    var isEmpty: Bool { !snapshot && !operations && !audit }

    mutating func merge(_ other: EventRefresh) {
        snapshot = snapshot || other.snapshot
        operations = operations || other.operations
        audit = audit || other.audit
    }
}
