@testable import AgenthailHelpers
import Foundation

@main
struct SessionLaunchTest {
    static func main() throws {
        let accepted = try decide(#"{"ok":true,"status":"submitted","accepted":true,"retryable":false,"launcher":"tmux"}"#)
        check(accepted == .submitted("Submitted to tmux. It appears in the sidebar once it starts."), "an accepted launch without a session stays on the sheet as submitted")

        let warned = try decide(#"{"ok":true,"status":"submitted","accepted":true,"launcher":"cmux","warning":"cmux did not report a pane"}"#)
        check(warned == .submitted("Submitted to cmux. It appears in the sidebar once it starts.\ncmux did not report a pane"), "the daemon's warning is shown")

        let replayed = try decide(#"{"ok":true,"status":"submitted"}"#)
        check(replayed == .starting, "a replayed in-flight create reads as still starting")

        let pending = try decide(#"{"ok":true,"launcher":"tmux","location":{"session":"agents","pane":"%1"}}"#)
        check(pending == .submitted("Submitted to tmux. It appears in the sidebar once it starts."), "a pending terminal launch reads as submitted, not as a replay")

        let chosen = try decide(#"{"ok":true,"status":"submitted","accepted":true}"#, launcher: "tmux")
        check(chosen == .submitted("Submitted to tmux. It appears in the sidebar once it starts."), "an unnamed target falls back to the chosen launcher")

        let fallback = try decide(#"{"ok":true,"status":"submitted","accepted":true}"#)
        check(fallback == .submitted("Submitted to claude. It appears in the sidebar once it starts."), "an unnamed target falls back to the agent")

        let opened = try decide(#"{"ok":true,"status":"submitted","accepted":true,"session":{"id":"s1","surface":"claude","name":"","status":"idle","lastActive":"2026-10-04T12:00:00Z"}}"#)
        check(opened == .open("s1"), "a receipt with a session opens it")

        let hostUnknown = try decide(#"{"error":{"code":"host_outcome_unknown","message":"the paired host never answered"}}"#)
        check(hostUnknown == .unconfirmed(nil), "an unanswered paired host stays unconfirmed")

        let storage = try decide(#"{"error":{"code":"create_storage_failed","message":"session was created, but local registration failed: disk full; do not retry automatically"},"session":{"id":"s3","surface":"claude","name":"","status":"idle","lastActive":"2026-10-04T12:00:00Z"}}"#)
        check(storage == .halted("session was created, but local registration failed: disk full; do not retry automatically"), "a created session with a storage failure halts with the daemon message")

        let ambiguous = try decide(#"{"error":{"code":"create_unknown","message":"initial turn outcome is ambiguous, but no session identity was returned; inspect the provider before any explicit retry: timeout"}}"#)
        check(ambiguous == .unconfirmed(nil), "an ambiguous outcome without a session stays unconfirmed")

        let failedCode = try decide(#"{"error":{"code":"create_failed","message":"launcher refused"}}"#)
        check(failedCode == .failed("launcher refused"), "a create_failed code is a definite failure")

        let initial = try decide(#"{"error":{"code":"initial_turn_failed","message":"initial message rejected"},"deliveryId":1,"session":{"id":"s4","surface":"claude","name":"","status":"idle","lastActive":"2026-10-04T12:00:00Z"}}"#)
        check(initial == .halted("initial message rejected"), "a created session whose first turn failed halts")

        let failed = try decide(#"{"error":{"code":"invalid_request","message":"no such folder"}}"#)
        check(failed == .failed("no such folder"), "a failure shows the daemon's error")
    }

    private static func decide(_ json: String, launcher: String? = nil) throws -> SessionLaunchDecision {
        let receipt = try JSONDecoder().decode(SessionCreationReceipt.self, from: Data(json.utf8))
        return SessionLaunchDecision(receipt, launcher: launcher, agent: "claude")
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
