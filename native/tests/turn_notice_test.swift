@testable import AgenthailHelpers
import Foundation

@main
struct TurnNoticeTest {
    static func main() {
        let notice = "What you write reaches this terminal session as a note from Agenthail, not as your own turn."
        func session(_ capabilities: Capabilities, readOnly: Bool = false) -> SessionState {
            SessionState(id: "T", surface: "claude", name: "t", alias: nil, status: "idle", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: capabilities, readOnly: readOnly, readOnlyReason: nil)
        }
        let adoptable = session(Capabilities(send: true, turnNotice: notice, lifecycle: SessionLifecycle(adopt: true)))
        check(adoptable.composerTurnNotice.hasPrefix(notice) && adoptable.composerTurnNotice.contains("Open in Agenthail"), "an adoptable session offers Open in Agenthail")
        let fixed = session(Capabilities(send: true, turnNotice: notice))
        check(fixed.composerTurnNotice == notice, "a session the app cannot adopt names no app remedy")
        check(session(Capabilities(send: true, lifecycle: SessionLifecycle(adopt: true))).composerTurnNotice.isEmpty, "a session that takes the person's turn has no notice")

        let older = #"{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":false,"interrupt":false,"steer":false}"#
        let decoded = try? JSONDecoder().decode(Capabilities.self, from: Data(older.utf8))
        check(decoded?.turnNotice == "", "a host that sends no notice decodes as none")
        let current = #"{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":false,"interrupt":false,"steer":false,"turnNotice":"note"}"#
        check((try? JSONDecoder().decode(Capabilities.self, from: Data(current.utf8)))?.turnNotice == "note", "a host's notice decodes")

        var dismissed = ""
        check(TurnNoticePreference.shows(for: "a", hidden: false, dismissed: dismissed), "a session shows the notice until it is dismissed")
        dismissed = TurnNoticePreference.dismissing("a", in: dismissed)
        check(!TurnNoticePreference.shows(for: "a", hidden: false, dismissed: dismissed), "dismissing hides it for that session")
        check(TurnNoticePreference.shows(for: "b", hidden: false, dismissed: dismissed), "dismissing one session leaves the others")
        check(!TurnNoticePreference.shows(for: "b", hidden: true, dismissed: dismissed), "don't show again hides it everywhere")
        dismissed = TurnNoticePreference.dismissing("a", in: dismissed)
        check(TurnNoticePreference.dismissed(dismissed) == ["a"], "dismissing twice records the session once")
        for index in 0..<(TurnNoticePreference.remembered + 5) { dismissed = TurnNoticePreference.dismissing("s\(index)", in: dismissed) }
        check(TurnNoticePreference.dismissed(dismissed).count == TurnNoticePreference.remembered && TurnNoticePreference.shows(for: "a", hidden: false, dismissed: dismissed), "only the most recent dismissals are remembered")
        let off = TurnNoticePreference.setting(shown: false, dismissed: dismissed)
        check(off.hidden && off.dismissed == dismissed, "turning the setting off hides the note and keeps per-session choices")
        let on = TurnNoticePreference.setting(shown: true, dismissed: dismissed)
        check(!on.hidden && TurnNoticePreference.shows(for: "s0", hidden: on.hidden, dismissed: on.dismissed) && TurnNoticePreference.shows(for: "s504", hidden: on.hidden, dismissed: on.dismissed), "turning it back on shows it on every session again")
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
