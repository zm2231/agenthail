import Foundation

// Filled by the stream reader without waiting on the main actor; drained all at once.
final class CatalogInbox: @unchecked Sendable {
    enum Contents {
        case events([CatalogStreamEvent])
        case overflow
    }

    private let lock = NSLock()
    private let bound: Int
    private let ready: @Sendable () -> Void
    private var pending: [CatalogStreamEvent] = []
    private var overflowed = false
    private var scheduled = false

    init(bound: Int, ready: @escaping @Sendable () -> Void) {
        self.bound = bound
        self.ready = ready
    }

    func append(_ event: CatalogStreamEvent) {
        let schedule: Bool = lock.withLock {
            guard !overflowed else { return false }
            if pending.count >= bound {
                pending.removeAll()
                overflowed = true
            } else {
                pending.append(event)
            }
            defer { scheduled = true }
            return !scheduled
        }
        if schedule { ready() }
    }

    func take() -> Contents {
        lock.withLock {
            scheduled = false
            if overflowed { return .overflow }
            defer { pending.removeAll(keepingCapacity: true) }
            return .events(pending)
        }
    }
}
