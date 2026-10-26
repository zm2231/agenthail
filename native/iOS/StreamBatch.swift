import os

final class StreamBatch<Event: Sendable>: Sendable {
    private struct State: Sendable {
        var events: [Event] = []
        var scheduled = false
    }

    private let state = OSAllocatedUnfairLock(initialState: State())
    private let drain: @MainActor @Sendable ([Event]) -> Void

    init(drain: @escaping @MainActor @Sendable ([Event]) -> Void) {
        self.drain = drain
    }

    func add(_ event: Event) {
        let schedule = state.withLock { state in
            state.events.append(event)
            defer { state.scheduled = true }
            return !state.scheduled
        }
        guard schedule else { return }
        Task { @MainActor in self.flush() }
    }

    @MainActor func flush() {
        let events = state.withLock { state in
            defer { state = State() }
            return state.events
        }
        if !events.isEmpty { drain(events) }
    }
}
