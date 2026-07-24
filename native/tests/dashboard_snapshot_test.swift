import Foundation

@main
struct DashboardSnapshotTest {
    static func snapshot(updatedAt: String, eventCursor: UInt64?, totalSessions: Int) -> DashboardSnapshot {
        DashboardSnapshot(
            updatedAt: updatedAt,
            eventCursor: eventCursor,
            daemon: DaemonState(running: true, pid: 1, stale: false, refreshError: nil),
            surfaces: [],
            sessions: [],
            totalSessions: totalSessions,
            queue: [],
            channels: [],
            relays: [],
            history: [],
            attention: [],
            codexRecentHours: 5
        )
    }

    static func main() {
        let current = snapshot(updatedAt: "2026-07-24T00:00:00Z", eventCursor: 12, totalSessions: 4)
        let transportOnlyChange = snapshot(updatedAt: "2026-07-24T00:00:30Z", eventCursor: 13, totalSessions: 4)
        let visibleChange = snapshot(updatedAt: "2026-07-24T00:00:30Z", eventCursor: 13, totalSessions: 5)
        precondition(current.hasSamePresentation(as: transportOnlyChange))
        precondition(!current.hasSamePresentation(as: visibleChange))
        print("dashboard snapshot tests passed")
    }
}
