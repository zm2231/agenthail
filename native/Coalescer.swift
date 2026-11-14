import Foundation

@MainActor
final class Coalescer {
    private let delay: Duration
    private let work: @MainActor () async -> Void
    private(set) var task: Task<Void, Never>?
    private var generation = 0
    private var running = false
    private var again = false

    init(delay: Duration, _ work: @escaping @MainActor () async -> Void) {
        self.delay = delay
        self.work = work
    }

    var pending: Bool { task != nil }

    func request() {
        guard task == nil else {
            if running { again = true }
            return
        }
        generation += 1
        let mine = generation
        let delay = delay
        task = Task { [weak self] in
            try? await Task.sleep(for: delay)
            guard let self, !Task.isCancelled, generation == mine else { return }
            running = true
            await work()
            guard generation == mine else { return }
            task = nil
            running = false
            if again {
                again = false
                request()
            }
        }
    }

    func cancel() {
        generation += 1
        task?.cancel()
        task = nil
        running = false
        again = false
    }
}
