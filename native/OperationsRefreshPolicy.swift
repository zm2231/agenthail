import Foundation

enum OperationsRefreshPolicy {
    static func reloadOperations(for eventType: String) -> Bool {
        eventType.hasPrefix("device.") || eventType == "settings.updated"
    }

    static func reloadAudit(for eventType: String) -> Bool {
        eventType != "stream.reset"
    }
}
