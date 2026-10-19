@testable import AgenthailHelpers
import Foundation

@main
struct LocalSendTest {
    static func main() {
        let sentAt = SessionTree.parseTimestamp("2026-10-04T12:00:00Z")!
        func user(_ id: String, _ text: String, _ at: String) -> TimelineItem {
            TimelineItem(id: id, kind: "message", role: "user", title: "user", text: text, timestamp: at, callId: nil, status: nil, truncated: false, truncationReason: nil, bodyRef: nil)
        }
        let first = LocalSend(text: "Run the tests", sentAt: sentAt, status: "Sent")
        let second = LocalSend(text: "Run the tests", sentAt: sentAt.addingTimeInterval(30), status: "Queued")

        let older = [user("a", "Run the tests", "2026-10-04T11:00:00Z")]
        expect(LocalSend.reconcile([first], with: older) == [first], "an older identical message does not absorb a new send")

        let one = [user("a", "Run the tests", "2026-10-04T11:00:00Z"), user("b", "Run the tests\n", "2026-10-04T12:00:01Z")]
        expect(LocalSend.reconcile([first, second], with: one) == [second], "one durable message absorbs exactly one local send")

        let both = one + [user("c", "Run the tests", "2026-10-04T12:00:31Z")]
        expect(LocalSend.reconcile([first, second], with: both).isEmpty, "each durable message absorbs one local send")

        let wrapped = "Another Claude session sent a message:\n<cross-session-message from=\"uds:/tmp/cc-socks/1.sock\" from-session=\"s\" from-name=\"agenthail/agenthail: operator\">\nRun the tests\n</cross-session-message>"
        expect(LocalSend.reconcile([first], with: [user("e", wrapped, "2026-10-04T12:00:02Z")]).isEmpty, "a send delivered inside a peer envelope absorbs its local copy")
        let other = wrapped.replacingOccurrences(of: "Run the tests", with: "Something else")
        expect(LocalSend.reconcile([first], with: [user("f", other, "2026-10-04T12:00:02Z")]) == [first], "a different peer message does not absorb the send")

        let assistant = [TimelineItem(id: "d", kind: "message", role: "assistant", title: "assistant", text: "Run the tests", timestamp: "2026-10-04T12:00:02Z", callId: nil, status: nil, truncated: false, truncationReason: nil, bodyRef: nil)]
        expect(LocalSend.reconcile([first], with: assistant) == [first], "assistant text never absorbs a user send")

        let notes = AttachedFile(name: "notes.txt", path: "/home/agent/.agenthail/uploads/abc/notes.txt", bytes: 16)
        let notesDraft = ComposerAttachment(name: "notes.txt", data: Data("synthetic notes\n".utf8), mediaType: "text/plain")
        let shotDraft = ComposerAttachment(name: "shot.png", data: Data("synthetic".utf8), mediaType: "image/png")
        func message(_ id: String, _ text: String, _ files: [AttachedFile]? = nil) -> TimelineItem {
            TimelineItem(id: id, kind: "message", role: "user", title: "user", text: text, timestamp: "2026-10-04T12:00:02Z", callId: nil, status: nil, truncated: false, truncationReason: nil, bodyRef: nil, files: files)
        }
        let image = TimelineItem(id: "h", kind: "attachment", role: "user", title: "Image", text: "Image attachment", timestamp: "2026-10-04T12:00:02Z", callId: nil, status: nil, truncated: false, truncationReason: nil, bodyRef: nil)

        let withFile = LocalSend(text: "Read this", attachments: [notesDraft], sentAt: sentAt, status: "Sent")
        expect(LocalSend.reconcile([withFile], with: [message("g", "Read this", [notes])]).isEmpty, "the delivered message with its file absorbs the send")
        expect(LocalSend.reconcile([withFile], with: [message("g", "Read this")]) == [withFile], "a same-text message without the file does not absorb a send with a file")
        expect(LocalSend.reconcile([first], with: [message("g", "Run the tests", [notes])]) == [first], "a message with a file does not absorb a send without one")
        let fileOnly = LocalSend(text: "", attachments: [notesDraft], sentAt: sentAt, status: "Sent")
        expect(LocalSend.reconcile([fileOnly], with: [image]) == [fileOnly], "an image row does not absorb a file-only send")
        expect(LocalSend.reconcile([fileOnly], with: [image, message("i", "", [notes])]).isEmpty, "a file-only send is absorbed by its file message")
        var renamed = LocalSend(text: "", attachments: [ComposerAttachment(name: "my notes.txt", data: Data("n".utf8), mediaType: "text/plain")], sentAt: sentAt, status: "Sent")
        renamed.uploaded = [UploadedAttachment(id: "upload:abc/my-notes.txt", name: "my-notes.txt", mediaType: "text/plain; charset=utf-8", bytes: 1)]
        expect(LocalSend.reconcile([renamed], with: [message("j", "", [AttachedFile(name: "my-notes.txt", path: "/u/abc/my-notes.txt")])]).isEmpty, "a file is matched by the name the daemon stored it under")
        let imageOnly = LocalSend(text: "", attachments: [shotDraft], sentAt: sentAt, status: "Sent")
        expect(LocalSend.reconcile([imageOnly], with: [image]).isEmpty, "an image-only send is absorbed by the image it delivered")
        expect(LocalSend.reconcile([imageOnly], with: [message("k", "", [AttachedFile(name: "shot.png", path: "/u/def/shot.png")])]).isEmpty, "an image sent as a file to a runtime without image input is absorbed by its file message")
        expect(LocalSend.reconcile([first], with: [image]) == [first], "an image never absorbs a text send")
        let textAndImage = LocalSend(text: "What is this?", attachments: [shotDraft], sentAt: sentAt, status: "Sent")
        expect(LocalSend.reconcile([textAndImage], with: [message("l", "What is this?"), image]).isEmpty, "a text and image send is absorbed by its text")
        expect(withFile.files == [AttachedFile(name: "notes.txt", bytes: 16)], "the local copy shows its attachments as files")

        expect(LocalSend.label(for: "queued") == "Queued" && LocalSend.label(for: "submitted") == "Submitted" && LocalSend.label(for: "sent") == "Sent", "receipt labels use the public vocabulary")
        expect(LocalSend.label(for: "queued", waitingOnYou: true) == "Queued · Waiting on you" && LocalSend.label(for: "sent", waitingOnYou: true) == "Sent", "a queued receipt to a session waiting on you says so")
        expect(LocalSend.label(for: "sent", evidence: "transport_accepted") == "Accepted · Not yet confirmed" && LocalSend.label(for: "sent", evidence: "delivered") == "Sent", "a steer only the transport accepted is not shown as sent")
        print("local send tests passed")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
