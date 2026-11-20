import XCTest

final class BodyRunsUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    private func runs(_ name: String, _ extra: [String], before: (XCUIApplication) -> Void = { _ in }) -> [String: Int] {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-theme", "dark", "--body-runs", "--preview-background-upserts"] + extra
        // Otherwise SwiftUI redraws every child until its background comparison setup is done, which a loaded host stretches past the window.
        app.launchEnvironment["AG_ASYNC_LAYOUTS"] = "0"
        app.launch()
        before(app)
        let probe = app.staticTexts["body-runs"]
        XCTAssertTrue(probe.waitForExistence(timeout: 30))
        Thread.sleep(forTimeInterval: 3)
        let before = Self.counts(probe.label)
        Thread.sleep(forTimeInterval: 10)
        let after = Self.counts(probe.label)
        var window = after.reduce(into: [String: Int]()) { $0[$1.key] = $1.value - (before[$1.key] ?? 0) }
        window["compare.missesSinceLaunch"] = after["compare.misses", default: 0]
        let runs = Self.foldRows(window)
        print(Self.line(name, runs))
        XCTAssertLessThanOrEqual(after["compare.misses", default: 0], 2, "SwiftUI could not compare unchanged views, so this measured its warm-up, not the app; check that AG_ASYNC_LAYOUTS=0 is still honoured")
        return runs
    }

    private static func foldRows(_ window: [String: Int]) -> [String: Int] {
        var folded = window
        let perRow = Dictionary(grouping: window.filter { $0.key.contains("#") && $0.value > 0 }) { String($0.key.prefix { $0 != "#" }) }
        for (name, rows) in perRow {
            folded[name + ".ids"] = rows.count
            folded[name + ".max"] = rows.map(\.value).max() ?? 0
            folded[name + ".over3"] = rows.filter { $0.value > 3 }.count
        }
        return folded
    }

    private static func counts(_ label: String) -> [String: Int] {
        Dictionary(label.split(separator: " ").compactMap { pair in
            let parts = pair.split(separator: "=")
            guard parts.count == 2, let value = Int(parts[1]) else { return nil }
            return (String(parts[0]), value)
        }, uniquingKeysWith: { $1 })
    }

    private static func line(_ name: String, _ runs: [String: Int]) -> String {
        "MEASURE \(name) " + runs.filter { !$0.key.contains("#") }.sorted { $0.key < $1.key }.map { "\($0.key)=\($0.value)" }.joined(separator: " ")
    }

    func testAnotherSessionsUpdatesLeaveTheOpenTranscriptAlone() {
        let runs = runs("transcript", ["--preview-reading"])
        XCTAssertGreaterThan(runs["upserts", default: 0], 50, "the background session kept updating")
        for view in ["SessionScreen", "IOSComposer", "IOSTimelineRow"] {
            XCTAssertLessThanOrEqual(runs[view, default: 0], 2, "\(view) redrew for another session's updates")
        }
    }

    func testStreamingIntoACrowdedTranscriptRedrawsOnlyTheGrowingMessage() {
        let runs = runs("streaming", ["--preview-stream", "--preview-stream-rate", "10"])
        let streamed = runs["streamed", default: 0]
        XCTAssertGreaterThan(streamed, 50, "the open session kept streaming")
        for view in ["CompactActivityGroup", "IOSTimelineRow"] {
            XCTAssertLessThanOrEqual(runs[view, default: 0], streamed * 2, "\(view) redrew rows the stream did not change")
        }
    }

    func testTextStreamingIntoAReplyLeavesTheScreenAlone() {
        let runs = runs("text-streaming", ["--preview-stream", "--preview-stream-rate", "10", "--preview-stream-shape", "text"])
        let streamed = runs["streamed", default: 0]
        XCTAssertGreaterThan(streamed, 50, "the open session kept streaming")
        XCTAssertLessThanOrEqual(runs["SessionScreen", default: 0], streamed * 2 / 3, "text growing in a reply redrew the whole screen")
    }

    func testAWorkersUpdatesRedrawOnlyItsCardInTheOpenNowSheet() {
        let runs = runs("now", ["--preview-reading", "--preview-crew"]) { app in
            let now = app.buttons["happening-now"]
            XCTAssertTrue(now.waitForExistence(timeout: 30))
            now.tap()
            XCTAssertTrue(app.buttons["crew-computer::crew-check"].waitForExistence(timeout: 10))
        }
        let upserts = runs["upserts", default: 0]
        XCTAssertGreaterThan(upserts, 50, "the worker kept updating")
        for view in ["SessionScreen", "IOSComposer", "IOSTimelineRow"] {
            XCTAssertLessThanOrEqual(runs[view, default: 0], 2, "\(view) redrew for a worker's updates")
        }
        XCTAssertGreaterThan(runs["PhoneCrewCard#computer::crew-check", default: 0], 0, "the updated worker's card redrew")
        let others = runs.filter { $0.key.hasPrefix("PhoneCrewCard#") && $0.key != "PhoneCrewCard#computer::crew-check" }
        XCTAssertFalse(others.isEmpty, "the other worker's card was counted")
        XCTAssertTrue(others.allSatisfy { $0.value == 0 }, "cards other than the updated worker's redrew: \(others)")
    }

    func testAnotherSessionsUpdatesRedrawOnlyItsRowInTheList() {
        let runs = runs("list", ["--preview-app"])
        let upserts = runs["upserts", default: 0]
        XCTAssertGreaterThan(upserts, 50, "the background session kept updating")
        for view in ["MainTabs", "ConversationListView"] {
            XCTAssertLessThanOrEqual(runs[view, default: 0], 2, "\(view) redrew for one session's update")
        }
        XCTAssertLessThanOrEqual(runs["IOSSessionRow", default: 0], upserts + 4, "rows other than the updated one redrew")
    }
}
