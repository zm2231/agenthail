import Foundation

enum SnapshotRefreshPolicy {
    private static let rereads: Set<String> = ["stream.reset", "state.changed", "settings.updated", "signin.changed", "host.paired", "host.removed", "host.state"]

    static func refreshes(for eventType: String) -> Bool { rereads.contains(eventType) }
}
