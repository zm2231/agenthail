@testable import AgenthailHelpers
import Foundation

@main
struct ToolPresentationTest {
    static func json(_ object: [String: Any]) -> String {
        String(decoding: try! JSONSerialization.data(withJSONObject: object), as: UTF8.self)
    }

    @MainActor
    static func main() {
        let edit = ToolPresentation(name: "Edit", text: json(["file_path": "/work/App.swift", "old_string": "a\nb\nc", "new_string": "a\nB\nc\nd\ne"]))
        check(edit.verb == "Edit" && edit.object.text == "App.swift" && edit.object.isCode, "an edit reads as Edit and its file name")
        check(edit.changes == [FileChange(path: "/work/App.swift", added: 3, removed: 1)], "an edit counts the lines it changed: \(edit.changes)")

        let write = ToolPresentation(name: "Write", text: json(["file_path": "/work/New.swift", "content": "one\ntwo"]))
        check(write.changes == [FileChange(path: "/work/New.swift", added: 2, removed: 0)], "a write adds every line")

        let terminated = ToolPresentation(name: "Write", text: json(["file_path": "/work/One.swift", "content": "one\n"]))
        check(terminated.changes == [FileChange(path: "/work/One.swift", added: 1, removed: 0)], "a final newline does not count as another line: \(terminated.changes)")
        check(FileChange.lines("").isEmpty && FileChange.lines("a\n\n").count == 2, "an empty text has no lines and a blank last line still counts")

        let multi = ToolPresentation(name: "MultiEdit", text: json(["file_path": "/work/M.swift", "edits": [["old_string": "x", "new_string": "y"], ["old_string": "", "new_string": "z"]]]))
        check(multi.changes == [FileChange(path: "/work/M.swift", added: 2, removed: 1)], "a multi-edit sums its edits: \(multi.changes)")

        let patch = "*** Begin Patch\n*** Update File: src/a.go\n@@\n-old\n+new\n+more\n*** Add File: src/b.go\n+package b\n*** End Patch"
        let applied = ToolPresentation(name: "apply_patch", text: json(["input": patch]))
        check(applied.changes == [FileChange(path: "src/a.go", added: 2, removed: 1), FileChange(path: "src/b.go", added: 1, removed: 0)], "a patch counts each file: \(applied.changes)")
        check(applied.verb == "Edit" && applied.object.text == "a.go, b.go", "a patch names its files")
        check(ToolPresentation(name: "apply_patch", text: patch).changes.count == 2, "a raw patch is read too")

        let merged = FileDiff.merged([
            FileDiff(path: "a", hunks: [TextDiff(before: "", after: "1")]),
            FileDiff(path: "b", hunks: [TextDiff(before: "x\ny", after: "X\nY")]),
            FileDiff(path: "a", hunks: [TextDiff(before: "1\n2\n3", after: "")]),
        ])
        check(merged.map(\.change) == [FileChange(path: "a", added: 1, removed: 3), FileChange(path: "b", added: 2, removed: 2)], "chips merge per file in first-touched order")
        check(merged[0].hunks.count == 2, "each call's change to a file stays its own hunk")

        let grep = ToolPresentation(name: "Grep", text: json(["pattern": "Collapse activity", "path": "/work/native"]))
        check(grep.verb == "Search" && grep.object.text == "Collapse activity", "a search names its pattern, not its folder")
        let described = ToolPresentation(name: "Bash", text: json(["command": "swift test\n--filter X", "description": "Run the tests"]))
        check(described.verb == "Run" && described.object.text == "Run the tests" && !described.object.isCode, "a described command reads as its description")
        let bare = ToolPresentation(name: "Bash", text: json(["command": "swift test\n--filter X"]))
        check(bare.object.text == "swift test" && bare.object.isCode, "a bare command shows its first line as code")
        check(ToolPresentation(name: "Read", text: json(["file_path": "/a/b.txt"])).object.text == "b.txt", "a read names its file")
        let created = ToolPresentation(name: "TaskCreate", text: json(["subject": "Parse the list", "description": "A long description of the work", "activeForm": "Parsing"]))
        check(created.verb == "Add task" && created.object.text == "Parse the list" && created.intent == nil && created.symbol == "checklist", "a new task reads by its subject: \(created.verb) \(created.object.text)")
        let updated = ToolPresentation(name: "TaskUpdate", text: json(["taskId": "2", "status": "in_progress"]))
        check(updated.verb == "Update task" && updated.object.text == "Task 2 · in progress", "a task update names the task and its status: \(updated.object.text)")
        check(ToolPresentation(name: "send_email", text: json(["subject": "Hi", "description": "Weekly note"])).object.text == "Weekly note", "other tools keep their description")

        func call(_ index: Int) -> TimelineItem {
            TimelineItem(id: "c\(index)", kind: "toolCall", role: nil, title: "Bash", text: "{}", timestamp: nil, callId: "c\(index)", status: nil, truncated: false, truncationReason: nil, bodyRef: nil)
        }
        check(!ToolRunSummary.folds(ToolRunSummary.entries((0..<6).map(call))), "six calls stay as lines")
        check(ToolRunSummary.folds(ToolRunSummary.entries((0..<7).map(call))), "seven calls fold into one summary line")

        let memory = TranscriptRowMemory()
        let first = TranscriptRows(memory: memory, session: "s1")
        first.set("row", open: true)
        check(first.isOpen("row") == true, "a row a person opened stays open")
        check(TranscriptRows(memory: memory, session: "s1").isOpen("row") == true, "coming back to the session finds it open")
        check(TranscriptRows(memory: memory, session: "s2").isOpen("row") == nil, "another session keeps its own rows")
        check(first == TranscriptRows(memory: memory, session: "s1") && first != TranscriptRows(memory: memory, session: "s2"), "row memory is equal only for the same session")
        first.set("row", body: RetainedBodyResult(text: "kept", error: nil))
        check(TranscriptRows(memory: memory, session: "s1").body("row")?.text == "kept", "a loaded retained body outlives the row that loaded it")
        check(TranscriptRows(memory: memory, session: "s2").body("row") == nil, "another session keeps its own bodies")

        var made = 0
        let stored = [FileDiff(path: "c", hunks: [TextDiff(before: "1", after: "2")])]
        let once = ToolDiffCache.diffs(name: "Edit", text: "cache-probe") { made += 1; return stored }
        let again = ToolDiffCache.diffs(name: "Edit", text: "cache-probe") { made += 1; return [] }
        check(once == stored && again == stored && made == 1, "a call's diffs are worked out once: made \(made)")
        _ = ToolDiffCache.diffs(name: "Edit", text: "cache-probe-2") { made += 1; return [] }
        check(made == 2, "a different call input is diffed on its own")
        let large = String(repeating: "x", count: 600_000)
        for n in 0..<12 { _ = ToolDiffCache.diffs(name: "Write", text: "\(n)" + large) { [] } }
        check(ToolDiffCache.retainedBytes <= ToolDiffCache.byteBudget, "large inputs stay within the byte budget: \(ToolDiffCache.retainedBytes)")
        var remade = 0
        _ = ToolDiffCache.diffs(name: "Write", text: "11" + large) { remade += 1; return [] }
        check(remade == 0, "the latest large input is still reused")
        _ = ToolDiffCache.diffs(name: "Write", text: String(repeating: "y", count: ToolDiffCache.byteBudget)) { [] }
        check(ToolDiffCache.retainedBytes <= ToolDiffCache.byteBudget, "an input larger than the budget is not kept")
        let manyLines = ToolPresentation(name: "Write", text: json(["file_path": "/work/Lines.txt", "content": String(repeating: "a\n", count: 300_000)]))
        check(manyLines.diffs.first?.hunks.first?.lines.count == 300_000, "a long write is diffed in full")
        var rediffed = 0
        _ = ToolDiffCache.diffs(name: "Write", text: manyLines.text) { rediffed += 1; return [] }
        check(rediffed == 1, "a short input whose diff exceeds the byte budget is not kept")
        let fewLines = ToolPresentation(name: "Write", text: json(["file_path": "/work/Few.txt", "content": "one\ntwo"]))
        let before = ToolDiffCache.retainedBytes
        _ = fewLines.diffs
        check(ToolDiffCache.retainedBytes - before == "Write".utf8.count + fewLines.text.utf8.count + ToolDiffCache.bytes(fewLines.diffs), "a kept write is charged for its input and its diff lines")
        check(edit.diffs == ToolPresentation(name: "Edit", text: edit.text).diffs, "the same edit reads back the same diff")
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
