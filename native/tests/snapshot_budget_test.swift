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
        final class Sleeps: @unchecked Sendable {
            var count = 0
        }
        func counting(_ budget: SnapshotBudget) -> (SnapshotBudget, Sleeps) {
            let sleeps = Sleeps()
            var counted = budget
            counted.sleep = { duration in
                sleeps.count += 1
                try? await Task.sleep(for: duration)
            }
            return (counted, sleeps)
        }

        let requested = ContinuousClock.now
        let budget = SnapshotBudget(deadlineMs: 900, reserve: .milliseconds(300))
        let started = ContinuousClock.now

        await budget.wait { false }
        let afterRevision = ContinuousClock.now - started
        check(afterRevision >= .milliseconds(550), "a revision that never arrives holds the first wait to the deadline, waited \(afterRevision)")
        let (afterDeadline, afterSleeps) = counting(budget)
        await afterDeadline.wait { false }
        await afterDeadline.pause(.milliseconds(300))
        check(afterSleeps.count == 0, "a window that never loads and the settle get nothing once the budget is spent, slept \(afterSleeps.count) times")
        check(budget.deadline >= requested + .milliseconds(600) && budget.deadline <= started + .milliseconds(600), "the answer goes out the reserve before the daemon stops waiting, deadline \(budget.deadline - requested) after arrival")

        let skewed = try JSONDecoder().decode(AgenthailEvent.self, from: Data("""
        {"id":1,"type":"ui.snapshot.requested","timestamp":"2027-10-23T08:00:00Z","data":{"id":"a","deadlineMs":20000}}
        """.utf8))
        let arriving = ContinuousClock.now
        let fromEvent = SnapshotBudget(deadlineMs: skewed.snapshot!.deadlineMs)
        let arrived = ContinuousClock.now
        check(fromEvent.deadline >= arriving + .seconds(18) && fromEvent.deadline <= arrived + .seconds(18), "a request stamped by a clock a year ahead still gets its full budget from arrival, got \(fromEvent.deadline - arriving)")

        let (ready, readySleeps) = counting(SnapshotBudget(deadlineMs: 20_000))
        await ready.wait { true }
        check(readySleeps.count == 0, "a wait whose condition holds returns at once, slept \(readySleeps.count) times")

        let cancelledAt = ContinuousClock.now
        let task = Task { @MainActor in await SnapshotBudget(deadlineMs: 20_000).wait { false } }
        try await Task.sleep(for: .milliseconds(100))
        task.cancel()
        await task.value
        check(ContinuousClock.now - cancelledAt < .seconds(10), "a closed request stops waiting when its work is cancelled rather than at its 18-second deadline, took \(ContinuousClock.now - cancelledAt)")

        let (spent, spentSleeps) = counting(SnapshotBudget(deadlineMs: 0))
        await spent.wait { false }
        await spent.pause(.seconds(1))
        check(spentSleeps.count == 0 && spent.remaining == .zero, "a budget with nothing left waits for nothing, slept \(spentSleeps.count) times")

        if failures > 0 { exit(1) }
        print("snapshot budget: every wait ends before the daemon stops waiting, whatever the clocks say")
    }
}
