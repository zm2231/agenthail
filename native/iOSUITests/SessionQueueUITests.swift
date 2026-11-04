import XCTest

@MainActor
final class SessionQueueUITests: XCTestCase {
    func testTwoQueuedRowsShowOpenWithTheirAttachmentsAndActions() {
        let app = launch(theme: "dark", rows: 2)
        XCTAssertTrue(app.buttons["session-queue-header"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["This is the failing screen, match the spacing."].exists)
        XCTAssertTrue(app.staticTexts["session-queue-attachments-11"].exists)
        XCTAssertTrue(app.buttons["session-queue-steer-11"].isHittable)
        XCTAssertFalse(app.buttons["session-queue-steer-12"].exists, "steer can't carry the row's model")
        XCTAssertEqual(app.staticTexts["session-queue-reason-12"].label, "Can't steer")
        XCTAssertFalse(app.staticTexts["Held: this agent is working"].exists, "the status above already says so")
        attach(app, "2 rows, dark")

        app.buttons["session-queue-menu-12"].tap()
        XCTAssertTrue(app.buttons["Edit"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["Cancel delivery"].exists)
        XCTAssertTrue(app.staticTexts["Steer can't carry this message's model or turn settings. It sends when the turn ends."].exists)
        attach(app, "Row menu with the reason it can't steer, dark")
    }

    func testTwoQueuedRowsReadInLightMode() {
        let app = launch(theme: "light", rows: 2)
        XCTAssertTrue(app.buttons["session-queue-header"].waitForExistence(timeout: 10))
        attach(app, "2 rows, light")
    }

    func testThreeQueuedRowsStartCollapsedAndOpenWithoutScrolling() {
        let app = launch(theme: "dark", rows: 3)
        XCTAssertTrue(app.buttons["session-queue-header"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["session-queue-menu-11"].exists)
        attach(app, "3 rows collapsed, dark")
        app.buttons["session-queue-header"].tap()
        XCTAssertTrue(app.buttons["session-queue-menu-13"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["session-queue-reason-13"].exists, "a reason the status doesn't give stays")
        attach(app, "3 rows open, dark")
    }

    func testFourQueuedRowsScrollInsideTheCap() {
        let app = launch(theme: "light", rows: 4)
        XCTAssertTrue(app.buttons["session-queue-header"].waitForExistence(timeout: 10))
        app.buttons["session-queue-header"].tap()
        let scroll = app.scrollViews["session-queue-scroll"]
        XCTAssertTrue(scroll.waitForExistence(timeout: 5))
        attach(app, "4 rows open with the scroll edge, light")
        let last = app.buttons["session-queue-menu-14"]
        let before = last.frame.minY
        scroll.swipeUp()
        XCTAssertLessThan(last.frame.minY, before, "the rows scroll inside the cap")
        XCTAssertTrue(last.isHittable, "every row's actions can be reached by scrolling")
        attach(app, "4 rows scrolled to the end, light")
    }

    private func launch(theme: String, rows: Int) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-session-queue", "-preview-session-queue-rows", String(rows), "--preview-theme", theme]
        app.launch()
        return app
    }

    private func attach(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
