@testable import AgenthailHelpers
import Foundation

@main
struct InboxStopCardTest {
    static func main() {
        let wrapping = "Run the migration only after the nightly backup finishes, then post the row counts before and after in this thread so we can compare them with last week."
        check(wrapping.count <= 280 && !wrapping.contains("\n"), "the case is a short single paragraph")
        check(InboxStopCard.overflows(wrapping, width: 240), "a short message that wraps past four lines in a narrow card can be shown in full")
        check(!InboxStopCard.overflows(wrapping, width: 1200), "the same message on one line needs no Show all")
        check(!InboxStopCard.overflows("Ship it.", width: 240), "a message that fits needs no Show all")
        check(InboxStopCard.overflows("one\ntwo\nthree\nfour\nfive", width: 1200), "five short lines are more than the card shows")
        check(!InboxStopCard.overflows(wrapping, width: 0), "before the card has a width nothing is offered")
        print("inbox stop card ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
