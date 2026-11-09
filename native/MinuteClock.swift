import Foundation
import Observation

// One timer for every row's age: a per-row TimelineView re-checks its schedule on every render pass.
@MainActor
@Observable
final class MinuteClock {
    static let shared = MinuteClock(ticking: true)

    private(set) var now: Date
    @ObservationIgnored private var timer: Timer?

    init(now: Date = Date(), ticking: Bool = false) {
        self.now = now
        if ticking { start() }
    }

    func tick(_ date: Date = Date()) {
        now = date
    }

    private func start() {
        let next = Calendar.current.nextDate(after: now, matching: DateComponents(second: 0), matchingPolicy: .nextTime) ?? now.addingTimeInterval(60)
        let timer = Timer(fire: next, interval: 60, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated { self?.tick() }
        }
        timer.tolerance = 1
        RunLoop.main.add(timer, forMode: .common)
        self.timer = timer
    }
}
