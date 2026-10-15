import Foundation

@main
struct DashboardSnapshotTest {
    static func snapshot(updatedAt: String, eventCursor: UInt64?, hostEpoch: String, catalogSeq: UInt64, totalSessions: Int) -> DashboardSnapshot {
        DashboardSnapshot(
            updatedAt: updatedAt,
            eventCursor: eventCursor,
            hostEpoch: hostEpoch,
            catalogSeq: catalogSeq,
            daemon: DaemonState(running: true, pid: 1, stale: false, refreshError: nil),
            surfaces: [],
            sessions: [],
            totalSessions: totalSessions,
            queue: [],
            channels: [],
            relays: [],
            history: [],
            attention: [],
            deliveryProblems: nil,
            codexRecentHours: 5,
            busyDelivery: "queue"
        )
    }

    static func main() {
        let current = snapshot(updatedAt: "2026-07-24T00:00:00Z", eventCursor: 12, hostEpoch: "host-a", catalogSeq: 12, totalSessions: 4)
        let transportOnlyChange = snapshot(updatedAt: "2026-07-24T00:00:30Z", eventCursor: 13, hostEpoch: "host-a", catalogSeq: 13, totalSessions: 4)
        let visibleChange = snapshot(updatedAt: "2026-07-24T00:00:30Z", eventCursor: 13, hostEpoch: "host-a", catalogSeq: 13, totalSessions: 5)
        precondition(current.hasSamePresentation(as: transportOnlyChange))
        precondition(!current.hasSamePresentation(as: visibleChange))
        let relayJSON = #"{"id":1,"from":"source","to":"target","pattern":"READY","handoff":"goal_completed","pendingCount":3,"lastError":"waiting for explicit goal completion"}"#.data(using: .utf8)!
        let relay = try! JSONDecoder().decode(RelayState.self, from: relayJSON)
        precondition(relay.handoff == "goal_completed" && relay.pendingCount == 3)
        precondition(relay.lastError == "waiting for explicit goal completion")
        print("dashboard snapshot tests passed")
    }
}
