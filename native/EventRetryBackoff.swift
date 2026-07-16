import Foundation

final class EventRetryBackoff: @unchecked Sendable {
    private let lock = NSLock()
    private var delay: TimeInterval = 1

    func nextDelay() -> TimeInterval {
        lock.lock()
        defer { lock.unlock() }
        let current = delay
        delay = min(delay * 2, 20)
        return current
    }

    func reset() {
        lock.lock()
        delay = 1
        lock.unlock()
    }
}
