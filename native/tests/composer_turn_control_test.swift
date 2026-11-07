@testable import AgenthailHelpers
import Foundation

@main
struct ComposerTurnControlTest {
    static func main() {
        let claudeModes = #"[{"id":"manual","label":"Manual"},{"id":"acceptEdits","label":"Accept edits"},{"id":"plan","label":"Plan"},{"id":"auto","label":"Auto"},{"id":"dontAsk","label":"Don't ask","confirm":true},{"id":"bypassPermissions","label":"Bypass permissions","confirm":true}]"#
        func mode(_ value: String, _ options: String) -> TurnSetting {
            try! JSONDecoder().decode(TurnSetting.self, from: Data(#"{"key":"mode","value":"\#(value)","options":\#(options),"applies":"now"}"#.utf8))
        }
        var seen: [String] = []
        var current = "manual"
        for _ in 0..<4 {
            current = mode(current, claudeModes).cycled?.id ?? ""
            seen.append(current)
        }
        check(seen == ["acceptEdits", "plan", "auto", "manual"], "⇧Tab cycles manual, accept edits, plan and auto: \(seen)")
        check(mode("", claudeModes).cycled?.id == "manual" && mode("bypassPermissions", claudeModes).cycled?.id == "manual" && mode("dontAsk", claudeModes).cycled?.id == "manual", "leaving a menu-only or unset mode starts the cycle again")
        check(mode("auto", claudeModes).quietOptions.allSatisfy { !$0.needsConfirmation } && mode("auto", claudeModes).quietOptions.count == 4, "cycling never lands on bypass permissions or don't ask")
        let codexModes = #"[{"id":"default","label":"Default"},{"id":"plan","label":"Plan"}]"#
        check(mode("default", codexModes).cycled?.id == "plan" && mode("plan", codexModes).cycled?.id == "default", "Codex switches between Default and Plan")

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
