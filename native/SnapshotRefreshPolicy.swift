import Foundation

enum SnapshotRefreshPolicy {
    static func refreshes(for eventType: String) -> Bool {
        ["stream.reset", "state.changed", "settings.updated"].contains(eventType) || eventType.hasPrefix("device.") || eventType.hasPrefix("host.")
    }
}
