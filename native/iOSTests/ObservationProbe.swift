import Observation

@MainActor
final class ObservationProbe {
    private let read: @MainActor () -> Void
    private var fired = false

    init(_ read: @escaping @MainActor () -> Void) {
        self.read = read
        arm()
    }

    func changed() -> Bool {
        guard fired else { return false }
        fired = false
        arm()
        return true
    }

    private func arm() {
        withObservationTracking { read() } onChange: { [weak self] in
            MainActor.assumeIsolated { self?.fired = true }
        }
    }
}
