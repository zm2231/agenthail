import XCTest
@testable import Agenthail

final class ActivityPresentationTests: XCTestCase {
    func testParallelCallsShareSummaryAndExpandWithMatchingResults() throws {
        let json = #"[{"id":"a","kind":"toolCall","title":"Bash","text":"{\"command\":\"swift test\"}","callId":"shell","truncated":false},{"id":"b","kind":"toolCall","title":"Read","text":"{\"file_path\":\"/project/config.yml\"}","callId":"read","truncated":false},{"id":"c","kind":"toolResult","title":"Result","text":"File missing","status":"error","callId":"read","truncated":false},{"id":"d","kind":"reasoning","title":"Thinking","text":"Check the workspace","truncated":false},{"id":"e","kind":"toolResult","title":"Result","text":"8 passed","callId":"shell","truncated":false},{"id":"f","kind":"message","role":"assistant","title":"Assistant","text":"Tests pass; config is missing.","truncated":false}]"#
        let items = try JSONDecoder().decode([TimelineItem].self, from: Data(json.utf8))
        let groups = TimelineGroup.make(items)
        XCTAssertEqual(groups.count, 2)
        XCTAssertEqual(groups[0].summary, "1 command, 1 file read (1 failed)")
        XCTAssertEqual(groups.flatMap(\.items), items)
        XCTAssertEqual(groups[0].invocations.map { $0.items.map(\.id) }, [["a", "e"], ["b", "c"], ["d"]])
        XCTAssertEqual(TimelineGroup.make(Array(items.prefix(2)))[0].id, groups[0].id)
    }

    func testWorkspaceSurvivesSnapshotAndSearchDecoding() throws {
        let snapshot = try JSONDecoder().decode(DashboardSnapshot.self, from: Data(SessionPreview.snapshotJSON.utf8))
        XCTAssertEqual(snapshot.sessions.first?.cwd, "/Users/demo/projects/fieldnotes")
        let json = #"{"results":[{"session":{"id":"saved","surface":"codex","name":"Release","cwd":"/project/release","status":"idle","queueCount":0,"open":false,"current":false,"capabilities":{"send":false,"stream":false,"reply":false,"goal":false,"compact":false,"model":false,"interrupt":false,"steer":false}},"snippet":"release"}],"remoteError":""}"#
        let result = try JSONDecoder().decode(SessionSearchResponse.self, from: Data(json.utf8))
        XCTAssertEqual(result.results.first?.session.cwd, "/project/release")
    }
}
