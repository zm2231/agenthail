@testable import AgenthailHelpers
import Foundation

@main
struct TranscriptRowActionTest {
    static func main() {
        var edited: [String] = []
        let first = TranscriptRowAction<String> { edited.append("first \($0)") }
        let second = TranscriptRowAction<String> { edited.append("second \($0)") }
        let none = TranscriptRowAction<String>(nil)

        check(first == second, "a fresh closure from another render keeps the row equal")
        check(first != none && none == TranscriptRowAction<String>(nil), "losing or gaining the action changes the row")

        second.closure?("text")
        check(edited == ["second text"], "the row calls the closure it was given last")
        check(none.closure == nil, "a row without the action has nothing to call")
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
