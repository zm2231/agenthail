import Foundation

/// Starts when the request arrives and ends before the daemon gives up on it, leaving time to upload the answer.
struct SnapshotBudget {
    static let reserve: Duration = .seconds(2)
    let deadline: ContinuousClock.Instant
    var sleep: @Sendable (Duration) async -> Void = { try? await Task.sleep(for: $0) }

    init(deadlineMs: Int64, reserve: Duration = SnapshotBudget.reserve) {
        deadline = ContinuousClock.now + .milliseconds(deadlineMs) - reserve
    }

    var remaining: Duration { max(.zero, deadline - ContinuousClock.now) }

    @MainActor
    func wait(until done: () -> Bool) async {
        while !done(), !Task.isCancelled, ContinuousClock.now < deadline {
            await sleep(min(.milliseconds(50), remaining))
        }
    }

    func pause(_ duration: Duration) async {
        let length = min(duration, remaining)
        if length > .zero { await sleep(length) }
    }
}
