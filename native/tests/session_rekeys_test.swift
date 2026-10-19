@testable import AgenthailHelpers
import Foundation

@main
struct SessionRekeysTest {
    static func main() {
        var rekeys = SessionRekeys()
        guard rekeys.isEmpty, rekeys.resolve("session_old") == "session_old" else { exit(1) }
        rekeys.record(["session_old": "conversation-a", "session_other": "conversation-b"])
        guard rekeys.resolve("session_old") == "conversation-a", rekeys.resolve("conversation-a") == "conversation-a" else { exit(2) }
        var drafts = ["session_old": "unsent", "session_other": "stale", "conversation-b": "current", "unrelated": "kept"]
        guard rekeys.carry(&drafts) else { exit(3) }
        guard drafts == ["conversation-a": "unsent", "conversation-b": "current", "unrelated": "kept"] else { exit(4) }
        guard !rekeys.carry(&drafts) else { exit(5) }
    }
}
