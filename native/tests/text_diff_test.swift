@testable import AgenthailHelpers
import SwiftUI

@main
struct TextDiffTest {
    @MainActor
    static func main() {
        let edit = TextDiff(before: "a\nb\nc", after: "a\nB\nc\nd\ne")
        check(edit.lines.map(\.kind) == [.same, .removed, .added, .same, .added, .added], "lines match in order, removals before their replacements: \(edit.lines.map(\.kind))")
        check(edit.added == 3 && edit.removed == 1, "an edit counts what it added and removed")

        let renamed = TextDiff(before: "let x = 1", after: "let y = 1")
        guard case .line(let old) = renamed.rows()[0], case .line(let new) = renamed.rows()[1] else { return fail("a changed line shows removed then added") }
        check(old.marks == [4..<5] && new.marks == [4..<5], "only the changed word is marked: \(old.marks) \(new.marks)")
        check(renamed.lines.allSatisfy { $0.marks.isEmpty }, "marks are worked out when the diff is shown, not when it is counted")

        let widened = TextDiff(before: "call(a)", after: "call(a, b)")
        guard case .line(let wider) = widened.rows()[1] else { return fail("an added line") }
        check(wider.marks == [6..<9], "an insertion inside a line marks just what was inserted: \(wider.marks)")

        let retyped = TextDiff(before: "    let changes: [FileChange]", after: "    let run: String\n    let diffs: [FileDiff]").rows()
        guard case .line(let first) = retyped[1], case .line(let second) = retyped[2] else { return fail("two added lines") }
        check(first.marks.isEmpty && !second.marks.isEmpty, "a removed line pairs with the added line most like it: \(first.marks) \(second.marks)")

        let replaced = TextDiff(before: "alpha", after: "omega")
        check(replaced.rows().allSatisfy { if case .line(let line) = $0 { return line.marks.isEmpty }; return true }, "a line with nothing in common is a replacement and is left unmarked")

        let accents = TextDiff(before: "café 👍🏽 old", after: "café 👍🏽 new")
        guard case .line(let accented) = accents.rows()[1] else { return fail("an added line") }
        let attributed = accented.attributed(marking: .green)
        let marked = attributed.runs.filter { $0.backgroundColor != nil }.map { String(attributed[$0.range].characters) }
        check(marked == ["new"], "marks count characters, so accents and emoji do not shift them: \(marked)")

        let body = (1...20).map { "line \($0)" }
        var changed = body
        changed[9] = "line ten"
        let folded = TextDiff(before: body.joined(separator: "\n"), after: changed.joined(separator: "\n")).rows(context: 3)
        check(folded.first == .folded(6), "unchanged lines before the first change fold down to the context: \(String(describing: folded.first))")
        check(folded.last == .folded(7), "unchanged lines after the last change fold too: \(String(describing: folded.last))")
        check(folded.count == 1 + 3 + 2 + 3 + 1, "a change keeps three lines of context on each side: \(folded.count)")
        check(TextDiff(before: "a\nb\nc\nd", after: "a\nB\nc\nd").rows(context: 3).count == 5, "a stretch no longer than the context stays on screen")

        let patch = FileDiff.patch("*** Begin Patch\n*** Update File: a.go\n@@ func a\n keep\n-old\n+new\n@@ func b\n-gone\n*** Delete File: c.go\n*** End Patch")
        check(patch.map(\.path) == ["a.go", "c.go"] && patch[0].hunks.count == 2, "a patch keeps each file and each section: \(patch.map(\.path)) \(patch.first?.hunks.count ?? 0)")
        check(patch[0].change == FileChange(path: "a.go", added: 1, removed: 2) && patch[1].change == FileChange(path: "c.go", added: 0, removed: 0), "patch counts come from its lines")

        let entries = DiffEntry.entries(patch[0].hunks)
        check(entries.map(\.id) == Array(0..<entries.count) && entries.contains { $0.content == .hunk }, "entries are numbered across hunks with a break between them")

        let write = ToolPresentation(name: "Write", text: #"{"file_path":"/w/N.swift","content":"one\ntwo\n"}"#)
        check(write.diffs.first?.hunks.first?.lines.map(\.kind) == [.added, .added], "a write shows every line as added")
    }

    static func fail(_ message: String) {
        print("FAIL: \(message)")
        exit(1)
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else { return fail(message) }
    }
}
