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

        let assistant = [TimelineItem(id: "d", kind: "message", role: "assistant", title: "assistant", text: "Run the tests", timestamp: "2026-10-04T12:00:02Z", callId: nil, status: nil, truncated: false, truncationReason: nil, bodyRef: nil)]
        expect(LocalSend.reconcile([first], with: assistant) == [first], "assistant text never absorbs a user send")

        expect(LocalSend.label(for: "queued") == "Queued" && LocalSend.label(for: "submitted") == "Submitted" && LocalSend.label(for: "sent") == "Sent", "receipt labels use the public vocabulary")
        print("local send tests passed")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
