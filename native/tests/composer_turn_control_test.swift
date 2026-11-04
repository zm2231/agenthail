@testable import AgenthailHelpers
import Foundation

@main
struct ComposerTurnControlTest {
    static func main() {
        var seen: [String] = []
        var mode = "manual"
        for _ in 0..<4 {
            mode = SessionRequests.cycled(from: mode)
            seen.append(mode)
        }
        check(seen == ["acceptEdits", "plan", "auto", "manual"], "⇧Tab cycles manual, accept edits, plan and auto: \(seen)")
        check(SessionRequests.cycled(from: "") == "acceptEdits" && SessionRequests.cycled(from: "default") == "acceptEdits", "a session in its settings' default moves on to accept edits")
        check(SessionRequests.cycled(from: "bypassPermissions") == "manual" && SessionRequests.cycled(from: "dontAsk") == "manual", "leaving a menu-only mode goes back to manual")
        check(!SessionRequests.cyclingModes.contains("bypassPermissions") && !SessionRequests.cyclingModes.contains("dontAsk"), "cycling never lands on bypass permissions or don't ask")
        check(SessionRequests.cyclingModes.allSatisfy(SessionRequests.permissionModes.contains), "every cycled mode is one a session can be set to")

        check(TurnSettings.Mode.cycled(from: nil) == .plan && TurnSettings.Mode.cycled(from: .plan) == .default && TurnSettings.Mode.cycled(from: .default) == .plan, "Codex switches between Plan and Work")

        check(QueueTakeBack.text(queued: ["first", "second"], draft: "now") == "first\n\nsecond\n\nnow", "taken-back messages go ahead of the draft, oldest first")
        check(QueueTakeBack.text(queued: ["first"], draft: "  \n") == "first", "an empty draft adds nothing after them")
        check(QueueTakeBack.text(queued: [], draft: "now") == "now", "nothing taken back leaves the draft alone")

        func attached(_ id: String, _ name: String, missing: Bool? = nil) -> QueueAttachment {
            QueueAttachment(id: id, name: name, mediaType: "image/png", bytes: 10, missing: missing)
        }
        let kept = ComposerAttachments.takingBack([attached("a", "a.png"), attached("b", "b.png", missing: true)], to: [])
        check(kept.attachments.map(\.uploaded?.id) == ["a"] && kept.attachments[0].data.isEmpty && kept.attachments[0].byteCount == 10, "a taken-back attachment keeps its upload and has no bytes")
        check(kept.lost == ["b.png"], "a missing attachment is named, not restored")
        check(ComposerAttachments.lostNotice(kept.lost)?.contains("b.png") == true && ComposerAttachments.lostNotice([]) == nil, "only a loss is announced")
        let sameName = ComposerAttachments.takingBack([attached("a", "a.png"), attached("c", "a.png")], to: [])
        check(sameName.attachments.count == 2, "two uploads with one name are both kept")
        let again = ComposerAttachments.takingBack([attached("a", "a.png")], to: kept.attachments)
        check(again.attachments.count == 1 && again.lost.isEmpty, "the same upload is not added twice")
        let full = (0..<ComposerAttachments.maxCount).map { ComposerAttachment(name: "\($0)", data: Data([1]), mediaType: "text/plain") }
        let over = ComposerAttachments.takingBack([attached("z", "z.png")], to: full)
        check(over.attachments.count == ComposerAttachments.maxCount && over.lost == ["z.png"], "over the limit is named, not dropped silently")
        print("composer turn control: ok")
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
