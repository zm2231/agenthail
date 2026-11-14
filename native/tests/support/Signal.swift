import Foundation

@MainActor
final class Signal {
    private(set) var count = 0
    private var waiters: [UUID: (at: Int, continuation: CheckedContinuation<Bool, Never>)] = [:]

    func fire() {
        count += 1
        for (id, waiter) in waiters where waiter.at <= count {
            waiters[id] = nil
            waiter.continuation.resume(returning: true)
        }
    }

    // False when the count is not reached in time, so a missing event fails the test instead of hanging it.
    func reached(_ target: Int, within seconds: Double = 5) async -> Bool {
        if count >= target { return true }
        let id = UUID()
        return await withCheckedContinuation { continuation in
            waiters[id] = (target, continuation)
            Task { @MainActor [weak self] in
                try? await Task.sleep(for: .seconds(seconds))
                self?.waiters.removeValue(forKey: id)?.continuation.resume(returning: false)
            }
        }
    }
}

@MainActor
func finished(_ task: Task<Void, Never>?, within seconds: Double = 5) async -> Bool {
    guard let task else { return true }
    let done = Signal()
    Task { @MainActor in
        await task.value
        done.fire()
    }
    return await done.reached(1, within: seconds)
}
