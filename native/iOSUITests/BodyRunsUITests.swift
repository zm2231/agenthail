import XCTest

final class BodyRunsUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    private func runs(_ extra: [String]) -> [String: Int] {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-theme", "dark", "--body-runs", "--preview-background-upserts"] + extra
        app.launch()
        let probe = app.staticTexts["body-runs"]
        XCTAssertTrue(probe.waitForExistence(timeout: 30))
        Thread.sleep(forTimeInterval: 3)
        let before = Self.counts(probe.label)
        Thread.sleep(forTimeInterval: 10)
        let after = Self.counts(probe.label)
        return after.reduce(into: [:]) { $0[$1.key] = $1.value - (before[$1.key] ?? 0) }
    }

    private static func counts(_ label: String) -> [String: Int] {
        Dictionary(label.split(separator: " ").compactMap { pair in
            let parts = pair.split(separator: "=")
            guard parts.count == 2, let value = Int(parts[1]) else { return nil }
            return (String(parts[0]), value)
        }, uniquingKeysWith: { $1 })
    }

    private static func line(_ name: String, _ runs: [String: Int]) -> String {
        "MEASURE \(name) " + runs.sorted { $0.key < $1.key }.map { "\($0.key)=\($0.value)" }.joined(separator: " ")
    }

    func testAnotherSessionsUpdatesLeaveTheOpenTranscriptAlone() {
        let runs = runs(["--preview-reading"])
        print(Self.line("transcript", runs))
        XCTAssertGreaterThan(runs["upserts", default: 0], 50, "the background session kept updating")
        for view in ["SessionScreen", "IOSComposer", "IOSTimelineRow"] {
            XCTAssertLessThanOrEqual(runs[view, default: 0], 2, "\(view) redrew for another session's updates")
        }
    }

    func testStreamingIntoACrowdedTranscriptRedrawsOnlyTheGrowingMessage() {
        let runs = runs(["--preview-stream", "--preview-stream-rate", "10"])
        print(Self.line("streaming", runs))
        let streamed = runs["streamed", default: 0]
        XCTAssertGreaterThan(streamed, 50, "the open session kept streaming")
        for view in ["CompactActivityGroup", "IOSTimelineRow"] {
            XCTAssertLessThanOrEqual(runs[view, default: 0], streamed * 2, "\(view) redrew rows the stream did not change")
        }
    }

    func testAnotherSessionsUpdatesRedrawOnlyItsRowInTheList() {
        let runs = runs(["--preview-app"])
        print(Self.line("list", runs))
        let upserts = runs["upserts", default: 0]
        XCTAssertGreaterThan(upserts, 50, "the background session kept updating")
        for view in ["MainTabs", "ConversationListView"] {
            XCTAssertLessThanOrEqual(runs[view, default: 0], 2, "\(view) redrew for one session's update")
        }
        XCTAssertLessThanOrEqual(runs["IOSSessionRow", default: 0], upserts + 4, "rows other than the updated one redrew")
    }
}
