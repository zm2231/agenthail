@testable import AgenthailHelpers
import AppKit
import Foundation

@main
struct SnapshotPaneUnseenTest {
    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }

    @MainActor
    static func main() {
        _ = NSApplication.shared
        func row(_ id: String, status: String) -> SessionState {
            SessionState(id: id, surface: "codex", name: id, alias: nil, status: status, lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(), readOnly: nil, readOnlyReason: nil)
        }
        func snapshot(_ status: String, at: String) -> DashboardSnapshot {
            DashboardSnapshot(updatedAt: at, eventCursor: nil, hostEpoch: nil, catalogSeq: nil, daemon: DaemonState(running: true, pid: nil, stale: nil, refreshError: nil, host: nil, hosts: nil), surfaces: [], sessions: [row("U", status: status)], totalSessions: 1, queue: [], channels: [], relays: [], history: [], codexRecentHours: 24, busyDelivery: nil)
        }
        let model = AgenthailModel(connecting: false)
        model.adopt(snapshot("busy", at: "1"))
        model.adopt(snapshot("idle", at: "2"))
        check(model.finishedUnseen.contains("U"), "a session that finished while nobody watched it is unseen")
        let pane = model.openPane()
        pane.marksSeen = false
        pane.select("U")
        check(model.finishedUnseen.contains("U"), "a pane drawn for a snapshot leaves the session unseen")
        model.adopt(snapshot("busy", at: "3"))
        model.adopt(snapshot("idle", at: "4"))
        check(model.finishedUnseen.contains("U"), "finishing while only a snapshot pane shows it leaves it unseen")
        model.closePane(pane)
        check(model.finishedUnseen.contains("U"), "closing the snapshot pane leaves it unseen")
        model.mainPane.select("U")
        check(!model.finishedUnseen.contains("U"), "the person's own pane marks it seen")
        print("snapshot pane unseen ok")
    }
}
