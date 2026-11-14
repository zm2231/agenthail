@testable import AgenthailHelpers
import Foundation

@main
struct SendQueueRekeyTest {
    @MainActor static var inFlight: [String: Int] = [:]
    @MainActor static var completed: [String: Int] = [:]
    @MainActor static var released: Set<String> = []
    @MainActor static var overlapped = false

    @MainActor
    static func main() async {
        let queue = SendQueue()
        queue.deliver = { send in
            inFlight[send.message, default: 0] += 1
            if inFlight[send.message, default: 0] > 1 { overlapped = true }
            while !released.contains(send.message), !Task.isCancelled { try? await Task.sleep(for: .milliseconds(10)) }
            inFlight[send.message, default: 0] -= 1
            if Task.isCancelled { return .unconfirmed }
            completed[send.message, default: 0] += 1
            return .answered
        }
        queue.add(PendingSend(sessionID: "old", message: "A", attachments: []))
        queue.add(PendingSend(sessionID: "new", message: "B", attachments: []))
        check(await wait { inFlight["A"] == 1 && inFlight["B"] == 1 }, "both sessions start delivering")

        var rekeys = SessionRekeys()
        rekeys.record(["old": "new"])
        queue.carry(rekeys)
        released.insert("B")
        check(await wait { completed["B"] == 1 }, "the send that did not move completes first")
        released.insert("A")
        check(await wait { queue.pending.isEmpty }, "every send is answered: \(queue.pending)")
        check(!overlapped, "a moved send is never delivered by two workers at once")
        check(completed == ["A": 1, "B": 1], "each send completes exactly once: \(completed)")
        print("ok")
    }

    @MainActor
    private static func wait(_ condition: () -> Bool) async -> Bool {
        for _ in 0..<200 {
            if condition() { return true }
            try? await Task.sleep(for: .milliseconds(10))
        }
        return condition()
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
