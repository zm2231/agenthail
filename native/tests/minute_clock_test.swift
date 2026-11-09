@testable import AgenthailHelpers
import Foundation
import Observation

@MainActor
final class Ticked {
    var hit = false
}

@main
struct MinuteClockTest {
    @MainActor
    static func main() {
        let start = Date(timeIntervalSince1970: 1_800_000_000)
        let clock = MinuteClock(now: start)
        let ticked = Ticked()
        withObservationTracking { _ = clock.now } onChange: { MainActor.assumeIsolated { ticked.hit = true } }
        check(!ticked.hit, "reading the time does not tick")
        clock.tick(start.addingTimeInterval(60))
        check(ticked.hit && clock.now == start.addingTimeInterval(60), "a tick tells every reader the new minute")

        let stamp = ISO8601DateFormatter().string(from: start.addingTimeInterval(-90))
        check(relativeAge(stamp, now: start) == "1m", "a row's age is measured from the clock's minute: \(relativeAge(stamp, now: start))")
        check(relativeAge(stamp, now: start.addingTimeInterval(3600)) == "1h", "and moves when the clock does")
        print("ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
