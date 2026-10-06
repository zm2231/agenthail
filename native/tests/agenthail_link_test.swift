import Foundation

@main
struct AgenthailLinkTest {
    static func main() {
        check(link("agenthail://") == .open, "bare scheme opens the app")
        check(link("agenthail://open") == .open, "open host opens the app")
        check(link("agenthail://new") == .newSession, "new starts a session")
        check(link("agenthail://session/019a-thread") == .session("019a-thread"), "session link carries the id")
        check(link("agenthail://session/@optimizer") == .session("@optimizer"), "session link carries an alias")
        check(link("agenthail://session/a%20b") == .session("a b"), "session reference is percent-decoded")
        check(link("agenthail://session") == nil, "session without a reference is rejected")
        check(link("agenthail://session/a/b") == nil, "extra path segments are rejected")
        check(link("agenthail://pair?endpoint=x&secret=y") == nil, "pairing links belong to the phone")
        check(link("https://session/a") == nil, "other schemes are rejected")
        for extra in ["agenthail://?x=1", "agenthail://open?x=1", "agenthail://new#x", "agenthail://session/a?x=1", "agenthail://session/a#x", "agenthail://user@session/a", "agenthail://session:1/a"] {
            check(link(extra) == nil, "\(extra) is rejected")
        }

        let session = SessionState(id: "019a-thread", surface: "codex", name: "", alias: "optimizer", status: "idle", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(), readOnly: false, readOnlyReason: nil, cwd: nil)
        check(AgenthailLink.matches(session, reference: "019a-thread"), "matches by id")
        check(AgenthailLink.matches(session, reference: "@optimizer"), "matches by alias with @")
        check(!AgenthailLink.matches(session, reference: "Optimizer"), "alias match is exact, like the daemon")
        check(!AgenthailLink.matches(session, reference: "other"), "rejects other references")
    }

    private static func link(_ text: String) -> AgenthailLink? {
        AgenthailLink(url: URL(string: text)!)
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
