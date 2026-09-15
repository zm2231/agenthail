import XCTest

@MainActor
final class ActivityReadingTests: XCTestCase {
    func testToolRunExpandsToCommandAndRecordedOutput() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session"]
        app.launch()
        let activity = app.buttons["activity-3"]
        XCTAssertTrue(activity.waitForExistence(timeout: 10))
        for _ in 0..<4 where !activity.isHittable { app.swipeDown() }
        XCTAssertTrue(activity.isHittable)
        capture("Chat with compact tools", app)
        activity.tap()
        let command = app.buttons.containing(.staticText, identifier: "Run command").firstMatch
        XCTAssertTrue(command.waitForExistence(timeout: 5))
        command.tap()
        let output = app.staticTexts["Test Suite ReconnectTests passed.\nExecuted 8 tests, with 0 failures."]
        for _ in 0..<4 where !output.isHittable { app.swipeUp() }
        XCTAssertTrue(output.exists)
        let collapseTool = app.buttons["collapse-tool-3"]
        app.swipeUp()
        app.swipeUp()
        XCTAssertTrue(collapseTool.isHittable)
        collapseTool.tap()
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: collapseTool)], timeout: 5), .completed)
        let collapseActivity = app.buttons["collapse-activity-3"]
        XCTAssertTrue(collapseActivity.isHittable)
        collapseActivity.tap()
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: command)], timeout: 5), .completed)
        capture("Recorded tool input and output", app)
        app.buttons["Session menu"].tap()
        XCTAssertTrue(app.buttons["All events"].waitForExistence(timeout: 5))
        app.buttons["All events"].tap()
        XCTAssertFalse(app.buttons["activity-3"].exists)
    }

    private func capture(_ name: String, _ app: XCUIApplication) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
