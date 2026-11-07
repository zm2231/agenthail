@testable import AgenthailHelpers
import Foundation
import Observation

@MainActor
@Observable
final class Flag {
    var set = false
}

@main
struct ObservationWaitTest {
    @MainActor
    static func main() {
        let flag = Flag()
        var answer: Bool?
        Task { @MainActor in answer = await waitUntil { flag.set } }
        RunLoop.main.run(until: Date().addingTimeInterval(0.05))
        check(answer == nil, "a wait ended before its condition held")
        flag.set = true
        check(spin { answer != nil }, "a wait did not end once its condition held")
        check(answer == true, "a wait whose condition held answered \(String(describing: answer))")

        let unset = Flag()
        var cancelled: Bool?
        let waiter = Task { @MainActor in cancelled = await waitUntil { unset.set } }
        RunLoop.main.run(until: Date().addingTimeInterval(0.05))
        waiter.cancel()
        check(spin { cancelled != nil }, "a cancelled wait did not end")
        check(cancelled == false, "a cancelled wait answered \(String(describing: cancelled))")

        let already = Flag()
        already.set = true
        var immediate: Bool?
        Task { @MainActor in immediate = await waitUntil { already.set } }
        check(spin { immediate != nil } && immediate == true, "a wait whose condition already held did not answer true")
        print("observation wait tests passed")
    }

    @MainActor
    static func spin(until done: () -> Bool) -> Bool {
        var passes = 0
        while !done(), passes < 1_000 {
            RunLoop.main.run(until: Date().addingTimeInterval(0.005))
            passes += 1
        }
        return done()
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
