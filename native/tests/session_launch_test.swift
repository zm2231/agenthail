import Foundation

@main
struct SessionLaunchTest {
    static func main() throws {
        let accepted = try decide(#"{"ok":true,"status":"submitted","accepted":true,"retryable":false,"launcher":"tmux"}"#)
        check(accepted == .submitted("Submitted to tmux. It appears in the sidebar once it starts."), "an accepted launch without a session stays on the sheet as submitted")
        check(accepted.settlesRetry, "an accepted launch clears the retry key")

        let warned = try decide(#"{"ok":true,"status":"submitted","accepted":true,"launcher":"cmux","warning":"cmux did not report a pane"}"#)
        check(warned == .submitted("Submitted to cmux. It appears in the sidebar once it starts.\ncmux did not report a pane"), "the daemon's warning is shown")

        let fallback = try decide(#"{"ok":true,"status":"submitted","accepted":true}"#, launcher: nil)
        check(fallback == .submitted("Submitted to claude. It appears in the sidebar once it starts."), "an unnamed target falls back to the agent")

        let opened = try decide(#"{"ok":true,"status":"submitted","accepted":true,"session":{"id":"s1","surface":"claude","name":"","status":"idle","lastActive":"2026-10-04T12:00:00Z"}}"#)
        check(opened == .open("s1"), "a receipt with a session opens it")
        check(opened.settlesRetry, "an opened session clears the retry key")

        let unknown = try decide(#"{"ok":false,"unknown":true,"sessionId":"s2"}"#)
        check(unknown == .unconfirmed("s2"), "an unknown outcome stays unconfirmed")
        check(!unknown.settlesRetry, "an unknown outcome keeps the retry key")

        let failed = try decide(#"{"ok":false,"error":"no such folder"}"#)
        check(failed == .failed("no such folder"), "a failure shows the daemon's error")
        check(!failed.settlesRetry, "a failure keeps the retry key")
    }

    private static func decide(_ json: String, launcher: String? = "tmux") throws -> SessionLaunchDecision {
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
