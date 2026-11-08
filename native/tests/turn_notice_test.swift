@testable import AgenthailHelpers
import Foundation

@main
struct TurnNoticeTest {
    static func main() {
        let notice = "What you write reaches this terminal session as a note from Agenthail, not as your own turn."
        func session(_ capabilities: Capabilities, readOnly: Bool = false) -> SessionState {
            SessionState(id: "T", surface: "claude", name: "t", alias: nil, status: "idle", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: capabilities, readOnly: readOnly, readOnlyReason: nil)
        }
        let remoteControl = " Run /remote-control in its terminal to write as yourself."
        let adopt = " Open in Agenthail also makes it your own turn."
        let vectors: [(String, Bool, Bool, String)] = [
            ("note", false, false, notice),
            ("note", true, false, notice + remoteControl),
            ("note", false, true, notice + adopt),
            ("note", true, true, notice + remoteControl + adopt),
            ("turn", true, true, ""),
            ("turn", false, false, ""),
        ]
        for (personMessage, remote, canAdopt, want) in vectors {
            let shown = session(Capabilities(send: true, personMessage: personMessage, remoteControl: remote, lifecycle: SessionLifecycle(adopt: canAdopt))).composerTurnNotice
            check(shown == want, "personMessage \(personMessage), remoteControl \(remote), adopt \(canAdopt): \(shown)")
        }
        let readOnly = session(Capabilities(send: true, personMessage: "note", lifecycle: SessionLifecycle(adopt: true)), readOnly: true).composerTurnNotice
        check(readOnly == notice, "a read-only session cannot be adopted, so it names no app remedy: \(readOnly)")

        let older = #"{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":false,"interrupt":false,"steer":false}"#
        let decoded = try? JSONDecoder().decode(Capabilities.self, from: Data(older.utf8))
        check(decoded?.personMessage == "turn" && decoded?.remoteControl == false, "a host that sends neither field takes the person's turn")
        let current = #"{"send":true,"stream":true,"reply":true,"goal":false,"compact":false,"model":false,"interrupt":false,"steer":false,"personMessage":"note","remoteControl":true}"#
        let host = try? JSONDecoder().decode(Capabilities.self, from: Data(current.utf8))
        check(host?.reachesAsNote == true && host?.remoteControl == true, "a host's personMessage and remoteControl decode")

        check(PersonMessageNote.receiptNote("note") == "Delivered as a note", "a send that went as a note says so")
        check(PersonMessageNote.receiptNote("turn") == nil && PersonMessageNote.receiptNote(nil) == nil, "a send that went as the person's turn, or is still queued, adds nothing")
        let receipt = #"{"evidence":"delivered","status":"sent","sessionId":"T","deliveredAs":"note"}"#
        let decodedReceipt = try? JSONDecoder().decode(DeliveryReceipt.self, from: Data(receipt.utf8))
        check(decodedReceipt?.deliveredAs == "note", "a send receipt's deliveredAs decodes")
        check(PersonMessageNote.receiptLine("Sent", receipt: decodedReceipt) == "Sent · Delivered as a note", "both clients put the note after the status")
        let withNotice = try? JSONDecoder().decode(DeliveryReceipt.self, from: Data(#"{"evidence":"delivered","status":"sent","deliveredAs":"note","notice":"Claude holds it"}"#.utf8))
        check(PersonMessageNote.receiptLine("Sent", receipt: withNotice) == "Sent · Delivered as a note · Claude holds it", "the receiver's notice follows the note on both clients")
        check(PersonMessageNote.receiptLine("Queued", receipt: nil) == "Queued", "no receipt adds nothing")

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
