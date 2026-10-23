@testable import AgenthailHelpers
import Foundation

@main
struct SnapshotBudgetTest {
    static func main() async throws {
        var failures = 0
        func check(_ condition: Bool, _ message: String) {
            if !condition {
                print("FAIL: \(message)")
                failures += 1
            }
        }
        let expiry = ContinuousClock.now + .milliseconds(900)
        let budget = SnapshotBudget(deadlineMs: 900, reserve: .milliseconds(300))
        let started = ContinuousClock.now

        await budget.wait { false }
        let afterRevision = ContinuousClock.now - started
        check(afterRevision >= .milliseconds(550), "a revision that never arrives holds the first wait to the deadline, waited \(afterRevision)")
        await budget.wait { false }
        await budget.pause(.milliseconds(300))
        let answered = ContinuousClock.now
        check(answered - started - afterRevision < .milliseconds(60), "a window that never loads and the settle get nothing once the budget is spent, took \(answered - started - afterRevision)")
        check(answered < expiry, "the answer goes out before the daemon stops waiting, \(expiry - answered) to spare")

        let skewed = try JSONDecoder().decode(AgenthailEvent.self, from: Data("""
        {"id":1,"type":"ui.snapshot.requested","timestamp":"2027-10-23T08:00:00Z","data":{"id":"a","deadlineMs":20000}}
        """.utf8))
        let fromEvent = SnapshotBudget(deadlineMs: skewed.snapshot!.deadlineMs)
        let left = fromEvent.remaining
        check(left > .milliseconds(17_900) && left <= .seconds(18), "a request stamped by a clock a year ahead still gets its full budget from arrival, got \(left)")

        let early = ContinuousClock.now
        await SnapshotBudget(deadlineMs: 20_000).wait { true }
        check(ContinuousClock.now - early < .milliseconds(20), "a wait whose condition holds returns at once")

        let cancelledAt = ContinuousClock.now
        let task = Task { @MainActor in await SnapshotBudget(deadlineMs: 20_000).wait { false } }
        try await Task.sleep(for: .milliseconds(100))
        task.cancel()
        await task.value
        check(ContinuousClock.now - cancelledAt < .milliseconds(400), "a closed request stops waiting when its work is cancelled, took \(ContinuousClock.now - cancelledAt)")

        let spent = SnapshotBudget(deadlineMs: 0)
        let late = ContinuousClock.now
        await spent.wait { false }
        await spent.pause(.seconds(1))
        check(ContinuousClock.now - late < .milliseconds(20) && spent.remaining == .zero, "a budget with nothing left waits for nothing")

        if failures > 0 { exit(1) }
        print("snapshot budget: every wait ends before the daemon stops waiting, whatever the clocks say")
    }
}
