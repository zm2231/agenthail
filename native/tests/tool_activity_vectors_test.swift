@testable import AgenthailHelpers
import Foundation

@main
struct ToolActivityVectorsTest {
    static let fixture = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        .appendingPathComponent("scripts/fixtures/tool-activity-vectors.json")

    static let calls: [(String, String)] = [
        ("Bash", #"{"command":"swift test --filter ReconnectTests","description":"Run the reconnect tests"}"#),
        ("Bash", #"{"command":"swift build\n--target Reconnect","timeout":120000}"#),
        ("Bash", #"{"command":"ls","description":"   "}"#),
        ("exec_command", #"{"cmd":"go test ./...","workdir":"/work"}"#),
        ("functions.exec_command", #"{"command":["bash","-lc","ls"],"cmd":"ls -la"}"#),
        ("Edit", #"{"file_path":"/work/Sources/App.swift","old_string":"a\nb\nc","new_string":"a\nB\nc\nd"}"#),
        ("edit", #"{"path":"/work/notes.md","oldText":"one","newText":"two"}"#),
        ("Write", #"{"file_path":"/work/New.swift","content":"one\ntwo\n"}"#),
        ("MultiEdit", #"{"file_path":"/work/M.swift","edits":[{"old_string":"x","new_string":"y"},{"old_string":"","new_string":"z\nw"}]}"#),
        ("apply_patch", #"{"input":"*** Begin Patch\n*** Update File: a/b.swift\n@@\n-old\n+new\n+more\n*** Add File: c.md\n+hello\n*** End Patch"}"#),
        ("apply_patch", "*** Begin Patch\n*** Delete File: gone.txt\n*** End Patch"),
        ("TodoWrite", #"{"todos":[{"content":"Verify","status":"in_progress"}]}"#),
        ("update_plan", #"{"plan":[{"step":"Ship","status":"pending"}]}"#),
        ("TodoWrite", #"{"note":"no list"}"#),
        ("Read", #"{"file_path":"/work/README.md","limit":40}"#),
        ("Grep", #"{"pattern":"Collapse activity","path":"/work/native"}"#),
        ("Glob", #"{"pattern":"**/*.swift"}"#),
        ("WebFetch", #"{"url":"https://example.com/docs","prompt":"Summarise"}"#),
        ("WebSearch", #"{"query":"swiftui scroll position"}"#),
        ("Agent", #"{"description":"Find the callers","prompt":"Look for every caller","subagent_type":"Explore"}"#),
        ("TaskCreate", #"{"subject":"Parse the list","description":"A long description","activeForm":"Parsing"}"#),
        ("TaskUpdate", #"{"taskId":"2","status":"in_progress"}"#),
        ("TaskUpdate", #"{"taskId":"3"}"#),
        ("SendMessage", "{\n  \"to\": \"@collab\",\n  \"summary\": \"\",\n  \"message\": \"Audit done\\nDetails follow\"\n}"),
        ("send_email", #"{"subject":"Hi","description":"Weekly note"}"#),
        ("mcp__xcode__build_sim", #"{"count":3,"flag":true}"#),
        ("mcp__server__tool_name", "{}"),
        ("custom", #"{"nested":{"b":[1,"x",null,false],"a":{}},"list":[],"n":1.5,"m":-0.25,"big":12345678901}"#),
        ("custom", #"{"quote":{"s":"a\"b\n\u0001é\t"}}"#),
        ("custom", "a custom payload\nsecond line"),
        ("custom", "first\r\nsecond"),
        ("custom", ""),
        ("custom", "[1,2]"),
    ]

    static func item(_ id: String, _ kind: String, turn: String? = nil, status: String? = nil, title: String? = nil, call: String? = nil) -> [String: Any] {
        var item: [String: Any] = ["id": id, "kind": kind, "title": title ?? kind, "text": "", "truncated": false]
        if let turn { item["turnId"] = turn }
        if let call { item["callId"] = call }
        if let status { item["status"] = status }
        return item
    }

    static let timelines: [(String, [[String: Any]], Bool)] = [
        ("a turn that ended normally", [item("c", "toolCall", turn: "t1"), item("d", "done", turn: "t1")], false),
        ("a cancelled turn", [item("c", "toolCall", turn: "t1"), item("d", "done", turn: "t1", status: "cancelled")], true),
        ("an aborted Codex turn", [item("c", "toolCall", turn: "t1"), item("d", "done", turn: "t1", status: "turn_aborted")], false),
        ("a failed turn", [item("c", "toolCall", turn: "t1"), item("d", "done", turn: "t1", status: "failed")], false),
        ("an interruption notice", [item("c", "toolCall", turn: "t1"), item("e", "event", title: "Turn interrupted")], true),
        ("a call still going", [item("c", "toolCall", turn: "t1"), item("r", "reasoning", turn: "t1")], true),
        ("a session gone quiet", [item("c", "toolCall", turn: "t1")], false),
        ("a later turn began", [item("c", "toolCall", turn: "t1"), item("m", "message", turn: "t2")], true),
        ("another turn's end", [item("c", "toolCall", turn: "t1"), item("d", "done", turn: "t0", status: "cancelled")], true),
        ("a call with no turn", [item("c", "tool"), item("d", "done", turn: "t1", status: "interrupted")], true),
        ("an end with no turn", [item("c", "command", turn: "t1"), item("d", "done")], true),
        ("an item with no turn", [item("c", "toolCall", turn: "t1"), item("m", "message")], true),
        ("a result after an injected event", [item("c", "toolCall", turn: "t1", call: "k"), item("m", "event", turn: "t1", title: "Task notification"), item("r", "toolResult", turn: "t1", call: "k")], true),
        ("a result after an injected event, idle", [item("c", "toolCall", turn: "t1", call: "k"), item("m", "message", turn: "t1"), item("r", "toolResult", turn: "t1", call: "k"), item("d", "done", turn: "t1", status: "cancelled")], false),
        ("another call's result", [item("c", "toolCall", turn: "t1", call: "k"), item("r", "toolResult", turn: "t1", call: "j")], true),
        ("a host without turn ids, working", [item("a", "toolCall", call: "a"), item("ar", "toolResult", call: "a"), item("b", "toolCall", call: "b"), item("m", "message")], true),
        ("a host without turn ids, a finished turn then a new one", [item("a", "toolCall", call: "a"), item("d", "done"), item("m", "message"), item("b", "toolCall", call: "b")], true),
        ("a host without turn ids, interrupted", [item("a", "toolCall", call: "a"), item("d", "done", status: "cancelled")], false),
        ("a host without turn ids, idle", [item("a", "toolCall", call: "a"), item("m", "message")], false),
        ("parallel calls", [item("a", "toolCall", turn: "t1"), item("b", "toolCall", turn: "t1"), item("d", "done", turn: "t1", status: "cancelled"), item("c", "toolCall", turn: "t2")], true),
    ]

    static let outputs: [String] = [
        "",
        "ok",
        (1...30).map { "line \($0)" }.joined(separator: "\n"),
        (1...24).map { "line \($0)" }.joined(separator: "\n") + "\n",
        String(repeating: "x", count: 5000),
        String(repeating: "👍🏽", count: 3100),
        "first\r\nsecond\nthird",
        "e\u{301}\n" + String(repeating: "é", count: 2999),
    ]

    static let alignments: [([String], [String], Int)] = [
        (["a", "b"], ["b", "a"], Alignment.cellLimit),
        (["x", "a", "y"], ["y", "a", "x"], Alignment.cellLimit),
        (["a", "b", "c"], ["c", "b", "a"], Alignment.cellLimit),
        (["k", "a", "b", "k"], ["k", "b", "a", "k"], Alignment.cellLimit),
        (["a", "x", "b", "x"], ["x", "a", "x", "b"], Alignment.cellLimit),
        ([], ["a"], Alignment.cellLimit),
        (["a"], [], Alignment.cellLimit),
        (["p", "a", "b", "s"], ["p", "b", "c", "s"], 4),
        (["p", "a", "b", "s"], ["p", "b", "c", "d", "s"], 4),
        (["a", "b"], ["b", "a"], 3),
    ]

    static let edits: [(String, String)] = [
        ("a\nb\nc", "a\nB\nc\nd\ne"),
        ("let x = 1", "let y = 1"),
        ("call(a)", "call(a, b)"),
        ("    let changes: [FileChange]", "    let run: String\n    let diffs: [FileDiff]"),
        ("alpha", "omega"),
        ("café 👍🏽 old", "café 👍🏽 new"),
        ((1...20).map { "line \($0)" }.joined(separator: "\n"), (1...20).map { $0 == 10 ? "line ten" : "line \($0)" }.joined(separator: "\n")),
        ("a\r\nb\r\n", "a\r\nc\r\n"),
        ("one two two three", "one two three three"),
        ("x = f(a, b)\ny = 2", "y = 2\nx = f(b, a)"),
        ("", "new\nfile\n"),
        ("gone\n", ""),
        (String(repeating: "x", count: TextDiff.markLimit + 1), String(repeating: "y", count: TextDiff.markLimit + 1)),
    ]

    static func row(_ entry: DiffEntry) -> [String: Any] {
        switch entry.content {
        case .line(let line):
            let kind = switch line.kind { case .same: "same"; case .added: "added"; case .removed: "removed" }
            return ["kind": kind, "text": line.text, "marks": line.marks.map { [$0.lowerBound, $0.upperBound] }]
        case .folded(let count): return ["folded": count]
        case .hunk: return ["hunk": true]
        }
    }

    static func vectors() throws -> [String: Any] {
        let presentations: [[String: Any]] = calls.map { name, text in
            let tool = ToolPresentation(name: name, text: text)
            let object = tool.object
            return [
                "name": name, "text": text,
                "verb": tool.verb, "object": object.text, "code": object.isCode,
                "summary": tool.summary, "input": tool.readableInput,
                "changes": tool.changes.map { ["path": $0.path, "added": $0.added, "removed": $0.removed] },
                "diffs": tool.diffs.map { ["path": $0.path, "rows": DiffEntry.entries($0.hunks).map(row)] },
            ]
        }
        let states: [[String: Any]] = try timelines.map { name, items, active in
            let decoded = try JSONDecoder().decode([TimelineItem].self, from: JSONSerialization.data(withJSONObject: items))
            let states = ToolCallStates(items: decoded, active: active)
            let calls = decoded.filter(ToolRunSummary.isCall)
            return ["name": name, "items": items, "active": active,
                    "states": Dictionary(uniqueKeysWithValues: calls.map { ($0.id, states[$0.id].rawValue) })]
        }
        let labels = Dictionary(uniqueKeysWithValues: [ToolCallState.running, .interrupted, .failed, .unrecorded, .later].map { ($0.rawValue, $0.label) })
        let previews: [[String: Any]] = outputs.map { text in
            ["text": text, "preview": ToolRunSummary.outputPreview(text), "lines": text.split(separator: "\n", omittingEmptySubsequences: false).count]
        }
        let steps: [[String: Any]] = alignments.map { old, new, limit in
            let names = Alignment.steps(old, new, limit: limit).map { step in
                switch step { case .same: "same"; case .removed: "removed"; case .added: "added" }
            }
            return ["old": old, "new": new, "limit": limit, "steps": names]
        }
        let diffs: [[String: Any]] = edits.map { before, after in
            ["before": before, "after": after, "rows": DiffEntry.entries([TextDiff(before: before, after: after)]).map(row)]
        }
        return ["calls": presentations, "states": states, "labels": labels, "outputs": previews, "alignments": steps, "diffs": diffs]
    }

    static func main() throws {
        let data = try JSONSerialization.data(withJSONObject: vectors(), options: [.prettyPrinted, .sortedKeys])
        if ProcessInfo.processInfo.environment["AGENTHAIL_WRITE_VECTORS"] == "1" {
            try (data + Data("\n".utf8)).write(to: fixture)
            print("wrote \(fixture.path)")
            return
        }
        let stored = try Data(contentsOf: fixture)
        let want = try JSONSerialization.jsonObject(with: data) as! NSDictionary
        let have = try JSONSerialization.jsonObject(with: stored) as! NSDictionary
        guard want == have else {
            print("FAIL: \(fixture.path) no longer matches ToolPresentation; rerun with AGENTHAIL_WRITE_VECTORS=1 and update the dashboard's js/tool-activity.js to match")
            exit(1)
        }
    }
}
