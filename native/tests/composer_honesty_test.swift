@testable import AgenthailHelpers
import Foundation

@main
struct ComposerHonestyTest {
    static func main() throws {
        checkStatusLine()
        checkSteerRefusal()
        try checkReceipts()
        checkEdit()
        print("ok")
    }

    private static func checkStatusLine() {
        typealias State = ComposerStatusLine.State
        check(ComposerStatusLine.text(State()) == "Ready to send.", "an idle composer says it is ready")
        check(ComposerStatusLine.text(State(waitingOnYou: true, turnNotice: "Turn notice", working: true)) == "This agent is waiting on you. Messages queue until you answer it.", "waiting on you comes first")
        check(ComposerStatusLine.text(State(turnNotice: "Messages reach this terminal when its turn ends.", working: true, hasMessage: true)) == "Messages reach this terminal when its turn ends.", "the session's turn notice comes next")
        check(ComposerStatusLine.text(State(working: true, stopShortcut: "⌘.")) == "This agent is working. Stop (⌘.) ends the current turn.", "a working agent with an empty composer names Stop")
        check(ComposerStatusLine.text(State(working: true)) == "This agent is working. Stop ends the current turn.", "Stop without a shortcut")
        check(ComposerStatusLine.text(State(working: true, hasMessage: true, followUpHint: "Sends after this turn")) == "Sends after this turn", "a typed message while working says where it goes")
        check(ComposerStatusLine.text(State(working: true, hasMessage: true, steerRefusal: "Attached images can't join a running turn on this session. Send queues them for next.", followUpHint: "Steers now")) == "Attached images can't join a running turn on this session. Send queues them for next.", "a refused steer says why instead of the steer hint")
        check(ComposerStatusLine.text(State(working: true, hasMessage: true, isCommand: true, followUpHint: "Steers now")) == "This agent is working. Stop ends the current turn.", "a command doesn't get the follow-up hint")
    }

    private static func checkSteerRefusal() {
        let image = ComposerAttachment(name: "shot.png", data: Data("p".utf8), mediaType: "image/png")
        let file = ComposerAttachment(name: "notes.txt", data: Data("n".utf8), mediaType: "text/plain")
        var capabilities = Capabilities(steer: true)
        check(QueueSteer.composerRefusal([], capabilities: capabilities) == nil, "no attachments can always steer")
        check(QueueSteer.composerRefusal([file], capabilities: capabilities)?.hasPrefix("Update Agenthail") == true, "an older computer can't steer attachments")
        capabilities.steerImages = false
        check(QueueSteer.composerRefusal([image, file], capabilities: capabilities)?.hasPrefix("Attached images can't join") == true, "images can't steer where the session refuses them")
        check(QueueSteer.composerRefusal([file], capabilities: capabilities) == nil, "files can steer where only images are refused")
        capabilities.steerImages = true
        check(QueueSteer.composerRefusal([image], capabilities: capabilities) == nil, "images can steer where the session takes them")
    }

    private static func checkReceipts() throws {
        let older = try JSONDecoder().decode(ActionReceipt.self, from: Data(#"{"ok":true,"result":{"deliveryId":3,"evidence":"delivered","status":"sent","queueId":null,"detail":"Sent to worker."}}"#.utf8))
        check(older.result?.notice == nil && LocalSend.label(for: older.result) == "Sent", "a receipt from a daemon without notice still decodes and reads Sent")
        let noticed = try JSONDecoder().decode(ActionReceipt.self, from: Data(#"{"ok":true,"result":{"deliveryId":4,"evidence":"delivered","status":"sent","queueId":null,"detail":"Sent to worker.","notice":"The peer is in plan mode; it replies once the plan is approved."}}"#.utf8))
        check(LocalSend.label(for: noticed.result) == "Sent · The peer is in plan mode; it replies once the plan is approved.", "the daemon's notice follows the receipt: \(LocalSend.label(for: noticed.result))")
        let accepted = try JSONDecoder().decode(ActionReceipt.self, from: Data(#"{"ok":true,"result":{"deliveryId":5,"evidence":"transport_accepted","status":"sent","queueId":null,"detail":"Sent to the Claude socket, not steered."}}"#.utf8))
        check(LocalSend.label(for: accepted.result) == "Sent to the Claude socket, not steered.", "a transport-only receipt shows its detail")
        let queued = try JSONDecoder().decode(ActionReceipt.self, from: Data(#"{"ok":true,"result":{"deliveryId":null,"evidence":"queued","status":"queued","queueId":9,"detail":"Queued."}}"#.utf8))
        check(LocalSend.label(for: queued.result) == "Queued" && LocalSend.label(for: queued.result, steer: true) == "Queued", "a plain queued receipt keeps its short label")
        check(LocalSend.label(for: nil, steer: true) == "Steer sent", "a steer with no result reads as a steer")
    }

    private static func checkEdit() {
        let draft = ComposerDraft()
        draft.edit("Run the tests")
        check(draft.text == "Run the tests" && draft.focusRequests == 1, "Edit fills an empty composer and asks for focus")
        draft.text = "my draft"
        let file = ComposerAttachment(name: "notes.txt", data: Data("n".utf8), mediaType: "text/plain")
        draft.edit("Earlier message", attachments: [file])
        check(draft.text == "Earlier message\n\nmy draft", "Edit puts the message above the draft: \(draft.text)")
        check(draft.attachments.map(\.name) == ["notes.txt"] && draft.focusRequests == 2, "Edit brings a refused message's files back")
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
