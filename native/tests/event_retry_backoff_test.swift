import Foundation

@main
struct EventRetryBackoffTest {
    static func main() {
        let backoff = EventRetryBackoff()
        let delays = (0..<7).map { _ in backoff.nextDelay() }
        guard delays == [1, 2, 4, 8, 16, 20, 20] else { exit(1) }
        backoff.reset()
        guard backoff.nextDelay() == 1 else { exit(1) }
    }
}
