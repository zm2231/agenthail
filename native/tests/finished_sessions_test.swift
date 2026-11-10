@testable import AgenthailHelpers
import Foundation

@main
struct FinishedSessionsTest {
    static func main() {
        let working = row("a", status: "busy")
        let idle = row("a", status: "idle")
        let background = row("a", status: "idle", background: true)
        let nobody: (String) -> Bool = { _ in false }

        var tracker = FinishedSessions()
        tracker.track(previous: [working], current: [idle], viewing: nobody)
        check(tracker.contains("a"), "a session that stops working is finished and unseen")

        tracker = FinishedSessions()
        tracker.track(previous: [background], current: [idle], viewing: nobody)
        check(tracker.contains("a"), "a session whose background work ends is finished and unseen")

        tracker = FinishedSessions()
        tracker.track(previous: [working], current: [idle], viewing: { $0 == "a" })
        check(!tracker.contains("a"), "a session being viewed when it stops is never marked")

        tracker = FinishedSessions()
        tracker.track(previous: [idle], current: [idle], viewing: nobody)
        check(!tracker.contains("a"), "a session that was not working is not marked")

        tracker = FinishedSessions()
        tracker.track(previous: [working], current: [idle], viewing: nobody)
        tracker.track(previous: [idle], current: [idle], viewing: nobody)
        check(tracker.contains("a"), "a finished session stays unseen across later snapshots")
        tracker.track(previous: [idle], current: [working], viewing: nobody)
        check(!tracker.contains("a"), "a session that works again is no longer finished")

        tracker = FinishedSessions()
        tracker.track(previous: [working], current: [idle], viewing: nobody)
        tracker.track(previous: [idle], current: [background], viewing: nobody)
        check(!tracker.contains("a"), "a session waiting on background work again is no longer finished")

        tracker = FinishedSessions()
        tracker.track(previous: [working, row("b", status: "busy")], current: [idle, row("b", status: "idle")], viewing: nobody)
        tracker.track(previous: [idle, row("b", status: "idle")], current: [row("b", status: "idle")], viewing: nobody)
        check(!tracker.contains("a") && tracker.contains("b"), "a session that leaves the list is forgotten and the rest are kept")

        tracker = FinishedSessions()
        tracker.track(previous: [working], current: [idle], viewing: nobody)
        tracker.markSeen("a")
        check(!tracker.contains("a"), "opening a finished session marks it seen")
        let before = tracker
        tracker.markSeen("absent")
        check(tracker == before, "marking an unknown session changes nothing")
        print("ok")
    }

    static func row(_ id: String, status: String, background: Bool = false) -> SessionState {
        var session = SessionState(id: id, surface: "codex", name: id, alias: nil, status: status, lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(), readOnly: nil, readOnlyReason: nil)
        if background { session.backgroundTasks = BackgroundTasks(running: true, source: "codex") }
        return session
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
