import Foundation

enum SnapshotRefreshPolicy {
    static func refreshes(for eventType: String) -> Bool {
        ["stream.reset", "state.changed", "settings.updated", "signin.changed"].contains(eventType) || eventType.hasPrefix("device.") || eventType.hasPrefix("host.")
    }
}
