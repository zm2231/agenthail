@testable import AgenthailHelpers
import Foundation

@main
struct ComposerTextSyncTest {
    @MainActor
    static func main() {
        let sync = ComposerTextSync("@c")
        check(sync.textToShow(bound: "@c") == nil, "an update before the keystroke reaches the binding leaves the typed text in the view")

        var bound = "@c"
        var shownDuringWrite: String?? = .none
        sync.viewChanged(to: "@co") { text in
            shownDuringWrite = .some(sync.textToShow(bound: bound))
            bound = text
        }
        check(shownDuringWrite == .some(nil), "an update while the binding is written leaves the typed text in the view")
        check(sync.textToShow(bound: bound) == nil && sync.agreed == "@co", "once the binding has it, nothing is replaced")

        check(sync.textToShow(bound: "") == "" && sync.agreed == "", "a cleared draft goes into the view")
        check(sync.textToShow(bound: "earlier prompt") == "earlier prompt", "a prompt from history goes into the view")
        check(sync.textToShow(bound: "earlier prompt") == nil, "the same text is not put in twice")
        print("ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
