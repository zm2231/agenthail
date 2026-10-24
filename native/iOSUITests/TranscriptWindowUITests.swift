import XCTest

@MainActor
final class TranscriptWindowUITests: XCTestCase {
    func testOlderRowsJoinAboveWithoutMovingTheReader() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-live-scroll", "--preview-reading", "--preview-long-history"]
        app.launch()
        let timeline = app.scrollViews["session-timeline"]
        XCTAssertTrue(timeline.waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["The latest update is visible above the composer."].waitForExistence(timeout: 10))
        XCTAssertFalse(app.staticTexts["Earlier step 1"].exists, "the oldest rows are not laid out on open")

        var oldest = Int.max
        for _ in 0..<40 where !app.buttons["context-context"].isHittable {
            timeline.swipeDown(velocity: .slow)
            let visible = visibleSteps(app, in: timeline)
            guard let top = visible.min() else { continue }
            XCTAssertLessThanOrEqual(top, oldest, "scrolling up never lands on newer rows than were on screen")
            if oldest != .max { XCTAssertGreaterThanOrEqual(top, oldest - 12, "rows joining above do not push the reader further back than a swipe moves") }
            oldest = top
        }
        XCTAssertTrue(app.buttons["context-context"].isHittable, "every older row joins as the reader reaches it")
        XCTAssertEqual(oldest, 1)
    }

    func testOpeningAtAnOldRowRevealsThroughIt() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-reading", "--preview-long-history", "--preview-reading-top"]
        app.launch()
        XCTAssertTrue(app.scrollViews["session-timeline"].waitForExistence(timeout: 10))
        let context = app.buttons["context-context"]
        XCTAssertTrue(context.waitForExistence(timeout: 10))
        XCTAssertTrue(context.isHittable)
        app.buttons["Jump to latest"].tapWhenReady()
        XCTAssertTrue(app.staticTexts["The latest update is visible above the composer."].isHittable)
    }

    private func visibleSteps(_ app: XCUIApplication, in timeline: XCUIElement) -> [Int] {
        let frame = timeline.frame
        return app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH 'Earlier step '")).allElementsBoundByIndex.compactMap { text in
            guard text.exists, frame.intersects(text.frame), text.frame.minY >= frame.minY else { return nil }
            return Int(text.label.dropFirst("Earlier step ".count))
        }
    }
}
