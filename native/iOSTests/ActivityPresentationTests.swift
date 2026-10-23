import XCTest
@testable import Agenthail

final class ActivityPresentationTests: XCTestCase {
    func testParallelCallsShareSummaryAndExpandWithMatchingResults() throws {
        let json = #"[{"id":"a","kind":"toolCall","title":"Bash","text":"{\"command\":\"swift test\"}","callId":"shell","truncated":false},{"id":"b","kind":"toolCall","title":"Read","text":"{\"file_path\":\"/project/config.yml\"}","callId":"read","truncated":false},{"id":"c","kind":"toolResult","title":"Result","text":"File missing","status":"error","callId":"read","truncated":false},{"id":"d","kind":"reasoning","title":"Thinking","text":"Check the workspace","truncated":false},{"id":"e","kind":"toolResult","title":"Result","text":"8 passed","callId":"shell","truncated":false},{"id":"f","kind":"message","role":"assistant","title":"Assistant","text":"Tests pass; config is missing.","truncated":false}]"#
        let items = try JSONDecoder().decode([TimelineItem].self, from: Data(json.utf8))
        let groups = TimelineGroup.make(items)
        XCTAssertEqual(groups.count, 2)
        XCTAssertEqual(ToolRunSummary.label(groups[0].items), "Ran 1 command and read 1 file")
        XCTAssertEqual(groups[0].errorCount, 1)
        XCTAssertFalse(groups[0].folds)
        XCTAssertEqual(groups.flatMap(\.items), items)
        XCTAssertEqual(groups[0].invocations.map { $0.items.map(\.id) }, [["a", "e"], ["b", "c"], ["d"]])
        XCTAssertEqual(TimelineGroup.make(Array(items.prefix(2)))[0].id, groups[0].id)
        let newestFirst = TimelineGroup.newestFirst(items)
        XCTAssertEqual(newestFirst.map(\.id), ["f", "a"])
        XCTAssertEqual(newestFirst.last?.invocations.map { $0.items.map(\.id) }, [["a", "e"], ["b", "c"], ["d"]])
    }

    func testLongRunsFoldAndChipsLeaveOutFailedChanges() throws {
        func call(_ id: String, _ title: String, _ input: [String: Any], status: String = "completed") -> [[String: Any]] {
            let text = String(decoding: try! JSONSerialization.data(withJSONObject: input), as: UTF8.self)
            return [["id": id, "kind": "toolCall", "title": title, "text": text, "callId": id, "truncated": false],
                    ["id": id + "-r", "kind": "toolResult", "title": "Result", "text": "done", "status": status, "callId": id, "truncated": false]]
        }
        func decode(_ records: [[String: Any]]) throws -> [TimelineItem] {
            try JSONDecoder().decode([TimelineItem].self, from: JSONSerialization.data(withJSONObject: records))
        }
        let six = try decode((0..<6).flatMap { call("b\($0)", "Bash", ["command": "go test ./case\($0)"]) })
        XCTAssertFalse(TimelineGroup.make(six)[0].folds, "six calls stay as lines")
        let seven = try decode((0..<7).flatMap { call("b\($0)", "Bash", ["command": "go test ./case\($0)"]) })
        XCTAssertTrue(TimelineGroup.make(seven)[0].folds, "a seventh call folds the run")

        let edits = try decode(
            call("e1", "Edit", ["file_path": "/work/App.swift", "old_string": "a", "new_string": "b\nc"])
            + call("e2", "Write", ["file_path": "/work/New.swift", "content": "one\n"])
            + call("e3", "Edit", ["file_path": "/work/Broken.swift", "old_string": "x", "new_string": "y"], status: "error")
            + call("e4", "Edit", ["file_path": "/work/App.swift", "old_string": "c", "new_string": ""])
        )
        XCTAssertEqual(TimelineGroup.make(edits)[0].changes, [
            FileChange(path: "/work/App.swift", added: 2, removed: 2),
            FileChange(path: "/work/New.swift", added: 1, removed: 0),
        ])
    }

    func testTurnDurationJoinsTheReplyItEnds() throws {
        let json = #"[{"id":"m","kind":"message","role":"assistant","title":"assistant","text":"Done.","timestamp":"2026-09-12T04:03:00Z","truncated":false},{"id":"d","kind":"event","title":"Turn duration","text":"1m55s","truncated":false},{"id":"u","kind":"message","role":"user","title":"user","text":"Next","truncated":false},{"id":"d2","kind":"event","title":"Turn duration","text":"12s","truncated":false}]"#
        let groups = TimelineGroup.make(try JSONDecoder().decode([TimelineItem].self, from: Data(json.utf8)))
        XCTAssertEqual(groups.map(\.id), ["m", "u", "d2"], "only a reply takes the duration that follows it")
        XCTAssertEqual(groups[0].finished, "1m55s")
        XCTAssertNil(groups[1].finished)
        XCTAssertEqual(TurnClock.clock("1m55s"), "1:55")
    }

    func testStreamedCodexReplyTakesItsTurnDuration() throws {
        let json = #"[{"id":"t","kind":"text","title":"Assistant","text":"Done.","timestamp":"2026-09-12T04:03:00Z","truncated":false},{"id":"d","kind":"event","title":"Turn duration","text":"42s","truncated":false},{"id":"p","kind":"text","role":"peer","title":"peer","text":"Ping","truncated":false},{"id":"d2","kind":"event","title":"Turn duration","text":"3s","truncated":false}]"#
        let groups = TimelineGroup.make(try JSONDecoder().decode([TimelineItem].self, from: Data(json.utf8)))
        XCTAssertEqual(groups.map(\.id), ["t", "p", "d2"], "a text reply without a role is the agent's and takes the duration; a peer's does not")
        XCTAssertEqual(groups[0].finished, "42s")
        XCTAssertTrue(groups[0].items[0].isSaid)
    }

    func testWorkspaceSurvivesSnapshotAndSearchDecoding() throws {
        let snapshot = try JSONDecoder().decode(DashboardSnapshot.self, from: Data(SessionPreview.snapshotJSON.utf8))
        XCTAssertEqual(snapshot.sessions.first?.cwd, "/Users/demo/projects/fieldnotes")
        let json = #"{"results":[{"session":{"id":"saved","surface":"codex","name":"Release","cwd":"/project/release","status":"idle","queueCount":0,"open":false,"current":false,"capabilities":{"send":false,"stream":false,"reply":false,"goal":false,"compact":false,"model":false,"interrupt":false,"steer":false}},"snippet":"release"}],"remoteError":""}"#
        let result = try JSONDecoder().decode(SessionSearchResponse.self, from: Data(json.utf8))
        XCTAssertEqual(result.results.first?.session.cwd, "/project/release")
    }
}
