import Foundation

/// Ends before the daemon gives up on the request, leaving time to upload the answer.
struct SnapshotBudget {
    static let reserve: Duration = .seconds(2)
    let deadline: ContinuousClock.Instant

    init(expiresAt: Int64, reserve: Duration = SnapshotBudget.reserve, now: Date = Date()) {
        let left = expiresAt - Int64(now.timeIntervalSince1970 * 1000)
        deadline = ContinuousClock.now + .milliseconds(left) - reserve
    }

    var remaining: Duration { max(.zero, deadline - ContinuousClock.now) }

    @MainActor
    func wait(until done: () -> Bool) async {
        while !done(), ContinuousClock.now < deadline {
            try? await Task.sleep(for: min(.milliseconds(50), remaining))
        }
    }

    func pause(_ duration: Duration) async {
        let length = min(duration, remaining)
        if length > .zero { try? await Task.sleep(for: length) }
    }
}
