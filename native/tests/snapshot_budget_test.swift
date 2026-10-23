@testable import AgenthailHelpers
import Foundation

@main
struct SnapshotBudgetTest {
    static func main() async {
        var failures = 0
        func check(_ condition: Bool, _ message: String) {
            if !condition {
                print("FAIL: \(message)")
                failures += 1
            }
        }
        let now = Date()
        let expiry = ContinuousClock.now + .milliseconds(900)
        let budget = SnapshotBudget(expiresAt: Int64(now.timeIntervalSince1970 * 1000) + 900, reserve: .milliseconds(300), now: now)
        let started = ContinuousClock.now

        await budget.wait { false }
        let afterRevision = ContinuousClock.now - started
        check(afterRevision >= .milliseconds(550), "a revision that never arrives holds the first wait to the deadline, waited \(afterRevision)")
        await budget.wait { false }
        await budget.pause(.milliseconds(300))
        let answered = ContinuousClock.now
        check(answered - started - afterRevision < .milliseconds(60), "a window that never loads and the settle get nothing once the budget is spent, took \(answered - started - afterRevision)")
        check(answered < expiry, "the answer goes out before the request expires, \(expiry - answered) to spare")

        let early = ContinuousClock.now
        await SnapshotBudget(expiresAt: Int64(Date().timeIntervalSince1970 * 1000) + 20_000).wait { true }
        check(ContinuousClock.now - early < .milliseconds(20), "a wait whose condition holds returns at once")

        let late = ContinuousClock.now
        let replayed = SnapshotBudget(expiresAt: 1)
        await replayed.wait { false }
        await replayed.pause(.seconds(1))
        check(ContinuousClock.now - late < .milliseconds(20) && replayed.remaining == .zero, "a budget already past its expiry waits for nothing")

        if failures > 0 { exit(1) }
        print("snapshot budget: every wait ends before the request expires")
    }
}
