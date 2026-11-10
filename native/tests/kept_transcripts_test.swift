@testable import AgenthailHelpers
import AppKit
import Foundation

@MainActor
final class BindLog {
    private(set) var binds: [(id: String, pane: SessionPane, attached: Bool)] = []

    func views() -> KeptTranscripts.Views {
        KeptTranscripts.Views(
            make: { [self] pane, session in
                binds.append((session.id, pane, false))
                let host = NSView()
                host.identifier = NSUserInterfaceItemIdentifier(session.id)
                return host
            },
            bind: { [self] host, pane, session in binds.append((session.id, pane, host.superview != nil)) }
        )
    }

    func frozenBindsInWindow(live: SessionPane) -> Int { binds.filter { $0.pane !== live && $0.attached }.count }

    func lastPane(_ id: String) -> SessionPane? { binds.last { $0.id == id }?.pane }
}

@main
struct KeptTranscriptsTest {
    static func session(_ id: String) -> SessionState {
        SessionState(id: id, surface: "claude", name: id.lowercased(), alias: nil, status: "idle", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(), readOnly: false, readOnlyReason: nil)
    }

    @MainActor
    static func main() {
        let ids = ["A", "B", "C", "D", "E", "F"]
        func listing(_ ids: [String]) -> DashboardSnapshot {
            DashboardSnapshot(updatedAt: "2026-10-04T12:00:00Z", eventCursor: 1, hostEpoch: "h", catalogSeq: 1, daemon: DaemonState(running: true, pid: 1, stale: false, refreshError: nil), surfaces: [], sessions: ids.map(session), totalSessions: ids.count, queue: [], channels: [], relays: [], history: [], codexRecentHours: 5, busyDelivery: "queue")
        }
        let model = AgenthailModel(connecting: false)
        model.snapshot = listing(ids)
        let pane = model.openPane()
        let kept = pane.keptTranscripts
        let container = NSView(frame: NSRect(x: 0, y: 0, width: 600, height: 400))
        let log = BindLog()
        func open(_ id: String) {
            pane.select(id)
            kept.show(session(id), live: pane, in: container, views: log.views())
        }

        open("A")
        open("B")
        let frozenA = log.lastPane("A")
        check(log.lastPane("B") === pane, "the shown transcript reads the live pane, so its actions reach the composer")
        check(kept.isFrozen("A") && frozenA !== pane && frozenA?.selectedSessionID == "A", "the transcript left behind is bound to a frozen copy of its own session")
        check(frozenA?.rowMemory === pane.rowMemory, "the frozen copy shares the live pane's row memory, so rebinding does not change what rows read")
        check(kept.host(for: "A")?.superview == nil && container.subviews == [kept.host(for: "B")!], "only the shown transcript is in the window")
        frozenA?.select("C")
        check(frozenA?.selectedSessionID == "A", "a frozen copy never loads another session")
        check(log.binds.contains { $0.pane === frozenA } && log.frozenBindsInWindow(live: pane) == 0, "a transcript leaves the window before it is bound to its frozen copy, so it starts no loads")
        let live = pane.frozenTranscript()
        check(live.timelineItems.map(\.id) == pane.timelineItems.map(\.id) && (live.detail == nil) == (pane.detail == nil) && live.olderItems == pane.olderItems && live.olderCursor == pane.olderCursor && live.loadingOlder == pane.loadingOlder && live.olderError == pane.olderError && live.detailStale == pane.detailStale && live.detailRefreshFailed == pane.detailRefreshFailed && live.detailLoadError == pane.detailLoadError && live.removedSession == pane.removedSession && live.inspectorShown == pane.inspectorShown, "a frozen copy carries everything the transcript reads")

        open("A")
        check(log.lastPane("A") === pane && !kept.isFrozen("A") && kept.isFrozen("B"), "returning to a kept transcript binds it to the live pane again and freezes the one left")
        check(container.subviews == [kept.host(for: "A")!], "the returning transcript is the one shown")

        for id in ["C", "D", "E", "F"] { open(id) }
        check(kept.keptIDs == ["F", "E", "D", "C"], "the cap keeps the four most recent transcripts: \(kept.keptIDs)")

        kept.relieveMemoryPressure()
        check(kept.keptIDs == ["F"] && container.subviews == [kept.host(for: "F")!], "memory pressure drops every kept transcript but the shown one: \(kept.keptIDs)")

        open("A")
        pane.openNewSession()
        check(kept.isFrozen("A"), "leaving a transcript for another page freezes it")

        open("B")
        model.snapshot = listing(ids.filter { $0 != "B" })
        pane.reconcile()
        check(pane.removedSession?.id == "B" && !kept.isFrozen("B"), "a removed session stays shown on the live pane")
        pane.closeRemovedSession()
        check(kept.isFrozen("B"), "closing a removed session freezes its transcript before the pane lets it go")
        check(log.frozenBindsInWindow(live: pane) == 0, "no path binds a frozen copy while its host is in the window")

        pane.close()
        check(kept.keptIDs.isEmpty && container.subviews.isEmpty, "closing the pane drops every kept transcript")
        print("kept transcripts tests passed")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
