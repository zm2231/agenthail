import Foundation

struct RefreshOrder {
    private var started: UInt64 = 0
    private var applied: UInt64 = 0
    private var settled: UInt64 = 0

    mutating func begin() -> UInt64 {
        started += 1
        return started
    }

    mutating func adopts(_ request: UInt64) -> Bool {
        guard request > applied else { return false }
        applied = request
        return true
    }

    mutating func settles(_ request: UInt64) -> Bool {
        guard request > settled else { return false }
        settled = request
        return true
    }
}

extension Error {
    var isCancellation: Bool {
        self is CancellationError || (self as? URLError)?.code == .cancelled
    }

    var isTimeout: Bool {
        (self as? URLError)?.code == .timedOut
    }
}
