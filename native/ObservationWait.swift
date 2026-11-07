import Observation

@MainActor
func waitUntil(_ condition: @escaping @MainActor () -> Bool) async -> Bool {
    while !Task.isCancelled {
        if condition() { return true }
        let gate = ObservationGate()
        await withTaskCancellationHandler {
            await withCheckedContinuation { continuation in
                gate.arm(continuation)
                withObservationTracking { _ = condition() } onChange: {
                    Task { @MainActor in gate.open() }
                }
            }
        } onCancel: {
            Task { @MainActor in gate.open() }
        }
    }
    return false
}

@MainActor
private final class ObservationGate {
    private var continuation: CheckedContinuation<Void, Never>?
    private var opened = false

    func arm(_ continuation: CheckedContinuation<Void, Never>) {
        if opened {
            continuation.resume()
        } else {
            self.continuation = continuation
        }
    }

    func open() {
        opened = true
        continuation?.resume()
        continuation = nil
    }
}
