@testable import AgenthailHelpers
import Foundation

@main
struct TranscriptRowsTest {
    @MainActor
    static func main() {
        let memory = TranscriptRowMemory()
        let first = TranscriptRows(memory: memory, session: "first")
        let second = TranscriptRows(memory: memory, session: "second")

        first.set("row-1", open: true)
        check(first.isOpen("row-1") == true, "a session remembers the row it opened")
        check(second.isOpen("row-1") == nil, "another session with the same row id has its own memory")

        let opened = first.choice(true)
        let pages = first.choice(500)
        check(first.current(opened) == true && first.current(pages) == 500, "a row's choice counts in the session it was made in")
        check(second.current(opened) == nil && second.current(pages) == nil, "a row kept across a session switch falls back to the new session's memory")

        second.set("row-1", open: false)
        check(first.isOpen("row-1") == true && second.isOpen("row-1") == false, "each session keeps its own choice for a shared id")
        check(TranscriptRows(memory: memory, session: "first").current(opened) == true, "switching back restores the row's choice")
        check(first == TranscriptRows(memory: memory, session: "first") && first != second, "rows are equal only for the same memory and session")

        first.set("output-1", count: 1000)
        check(first.count("output-1") == 1000, "a session remembers how much output was shown, not just that it was open")
        check(second.count("output-1") == nil, "another session's output starts from its preview")
    }

    private static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
