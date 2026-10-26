import XCTest

@MainActor
final class TranscriptLiveEdgeUITests: XCTestCase {
    private var captured: Bool { ProcessInfo.processInfo.environment["AGENTHAIL_PROBE_CAPTURE"] == "1" }
    private var sessionID: String { "session-computer::" + (ProcessInfo.processInfo.environment["AGENTHAIL_PROBE_SESSION"] ?? "demo") }

    private func launch(direct: Bool = false) -> XCUIApplication {
        let app = XCUIApplication()
        let stream = ProcessInfo.processInfo.environment["AGENTHAIL_PROBE_NOSTREAM"] == "1" ? ["--preview-follow"] : ["--preview-stream"]
        app.launchArguments = ["--preview-session", "--preview-theme", "dark"] + stream + (direct ? [] : ["--preview-app"]) + (captured ? ["--preview-capture"] : [])
        app.launch()
        return app
    }

    private func record(_ app: XCUIApplication, _ name: String) {
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }

    private func replies(_ app: XCUIApplication) -> [XCUIElement] {
        app.scrollViews["session-timeline"].staticTexts.matching(NSPredicate(format: "label CONTAINS 'of reply'")).allElementsBoundByIndex
    }

    private func newestReply(_ app: XCUIApplication) -> Int {
        replies(app).compactMap { element in
            element.label.components(separatedBy: "of reply ").dropFirst().first.flatMap { Int($0.prefix { $0.isNumber }) }
        }.max() ?? 0
    }

    private func visibleRows(_ app: XCUIApplication) -> Int {
        let viewport = app.scrollViews["session-timeline"].frame
        return app.scrollViews["session-timeline"].staticTexts.allElementsBoundByIndex.filter { viewport.intersects($0.frame) && !$0.frame.isEmpty }.count
    }

    func testReturningToASessionAfterAnotherLandsAtTheLiveEdge() {
        let app = launch()
        let session = app.buttons[sessionID]
        session.tapWhenReady(timeout: 15)
        XCTAssertTrue(app.buttons["Session menu"].waitForExistence(timeout: 10))
        sleep(3)
        let left = newestReply(app)
        XCTAssertGreaterThan(left, 0, "the session showed no reply before switching away")
        app.navigationBars.buttons.element(boundBy: 0).tapWhenReady()
        app.buttons["session-computer::codex-demo"].tapWhenReady()
        XCTAssertTrue(app.buttons["Session menu"].waitForExistence(timeout: 10))
        sleep(25)
        app.navigationBars.buttons.element(boundBy: 0).tapWhenReady()
        session.tapWhenReady()
        XCTAssertTrue(app.scrollViews["session-timeline"].waitForExistence(timeout: 5))
        var seen: [Int] = []
        let end = Date().addingTimeInterval(2.5)
        while Date() < end {
            let newest = newestReply(app)
            if newest > 0, seen.last != newest { seen.append(newest) }
            record(app, "return-\(seen.count)")
        }
        XCTAssertGreaterThan(seen.first ?? 0, left, "the returned session did not show the replies it caught up on")
        XCTAssertLessThanOrEqual(seen.count, 2, "the transcript walked through caught-up replies \(seen) instead of opening at the latest")
        XCTAssertFalse(app.buttons["Jump to latest"].exists, "a returned session opened away from its latest row")
    }

    func testJumpingToLatestWhileStreamingShowsTheLatestRows() {
        let app = launch(direct: captured)
        if !captured { app.buttons[sessionID].tapWhenReady(timeout: 15) }
        let timeline = app.scrollViews["session-timeline"]
        XCTAssertTrue(timeline.waitForExistence(timeout: 10))
        sleep(3)
        for _ in 0..<6 { timeline.swipeDown(velocity: .fast) }
        sleep(2)
        record(app, "before-jump")
        app.buttons["Jump to latest"].tapWhenReady()
        let end = Date().addingTimeInterval(3)
        var frame = 0
        while Date() < end {
            record(app, "jump-\(frame)")
            XCTAssertGreaterThan(visibleRows(app), 0, "the transcript was blank after Jump to latest")
            frame += 1
        }
    }
}
