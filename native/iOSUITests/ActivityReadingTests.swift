import XCTest

@MainActor
final class ActivityReadingTests: XCTestCase {
    func testToolRunExpandsToCommandAndRecordedOutput() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session"]
        app.launch()
        let timeline = app.scrollViews["session-timeline"]
        XCTAssertTrue(timeline.waitForExistence(timeout: 10))
        let command = app.buttons["tool-3"]
        XCTAssertTrue(command.waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["activity-3"].exists, "a short run shows its lines without a summary")
        for _ in 0..<4 where !command.isHittable { timeline.swipeDown() }
        XCTAssertTrue(command.isHittable)
        capture("Chat with compact tools", app)
        command.tapWhenReady()
        let output = app.staticTexts["Test Suite ReconnectTests passed.\nExecuted 8 tests, with 0 failures."]
        XCTAssertTrue(output.waitForExistence(timeout: 5))
        capture("Recorded tool input and output", app)
        let collapseTool = app.buttons["collapse-tool-3"]
        for _ in 0..<4 where !collapseTool.isHittable { timeline.swipeUp() }
        if collapseTool.frame.maxY > timeline.frame.maxY - 140 { timeline.swipeUp() }
        XCTAssertTrue(collapseTool.isHittable)
        collapseTool.tapWhenReady()
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: output)], timeout: 5), .completed)
        XCTAssertTrue(command.isHittable)
        app.buttons["Session menu"].tapWhenReady()
        app.buttons["All events"].tapWhenReady()
        XCTAssertTrue(app.buttons["record-4"].waitForExistence(timeout: 5), "every event lists a result as its own record")
    }

    func testReplyThatEndsATurnShowsHowLongItTook() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-reading"]
        app.launch()
        XCTAssertTrue(app.scrollViews["session-timeline"].waitForExistence(timeout: 10))
        let footer = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH 'Finished 1:55'")).firstMatch
        XCTAssertTrue(footer.waitForExistence(timeout: 10), "the reply that ends the turn shows how long it took")
        XCTAssertFalse(app.staticTexts.matching(NSPredicate(format: "label CONTAINS 'Turn Duration'")).firstMatch.exists, "the duration is not also listed as an event")
        capture("Reply with its turn footer", app)
    }

    private func capture(_ name: String, _ app: XCUIApplication) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
