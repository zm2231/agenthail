import os

final class StreamBatch<Event: Sendable>: Sendable {
    private struct State: Sendable {
        var events: [Event] = []
        var scheduled = false
    }

    private let state = OSAllocatedUnfairLock(initialState: State())
    private let drain: @MainActor @Sendable ([Event]) -> Void
    private let interval: Duration

    init(interval: Duration = .zero, drain: @escaping @MainActor @Sendable ([Event]) -> Void) {
        self.interval = interval
        self.drain = drain
    }

    func add(_ event: Event) {
        let schedule = state.withLock { state in
            state.events.append(event)
            defer { state.scheduled = true }
            return !state.scheduled
        }
        guard schedule else { return }
        Task { @MainActor in
            if self.interval > .zero { try? await Task.sleep(for: self.interval) }
            self.flush()
        }
    }

    @MainActor func flush() {
        let events = state.withLock { state in
            defer { state = State() }
            return state.events
        }
        if !events.isEmpty { drain(events) }
    }
}
