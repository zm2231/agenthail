import XCTest

@MainActor
final class SessionNavigationTests: XCTestCase {
    func testEmptyActivityStillShowsSavedMessages() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-rich", "--preview-history-only"]
        app.launch()
        XCTAssertTrue(app.staticTexts["Saved message history remains readable."].waitForExistence(timeout: 10))
        XCTAssertFalse(app.staticTexts["Ready for your instruction"].exists)
    }

    func testSessionOpensAndReturnsToBrowser() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app"]
        app.launch()
        let session = app.buttons["session-demo"]
        XCTAssertTrue(session.waitForExistence(timeout: 10))
        session.tap()
        XCTAssertTrue(app.buttons["Session menu"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.buttons["Stop current turn"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.tabBars.buttons["Inbox"].isHittable)
        app.navigationBars.buttons.element(boundBy: 0).tap()
        XCTAssertTrue(session.waitForExistence(timeout: 5))
        app.buttons["new-session"].tap()
        XCTAssertTrue(app.navigationBars["New session"].waitForExistence(timeout: 5))
    }

    func testInboxOpensTheCanonicalSessionBrowser() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app"]
        app.launch()
        app.tabBars.buttons["Inbox"].tap()
        let session = app.buttons["inbox-session-2"]
        XCTAssertTrue(session.waitForExistence(timeout: 10))
        session.tap()
        XCTAssertTrue(app.buttons["Session menu"].waitForExistence(timeout: 10))
        app.navigationBars.buttons.element(boundBy: 0).tap()
        XCTAssertTrue(app.navigationBars["Sessions"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.tabBars.buttons["Sessions"].isSelected)
        app.buttons["session-demo"].tap()
        XCTAssertTrue(app.buttons["Stop current turn"].waitForExistence(timeout: 10))
    }

    func testSessionInboxReturnsFromInspectorAndComposer() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-delivery"]
        app.launch()
        app.buttons["session-demo"].tap()
        let menu = app.buttons["Session menu"]
        XCTAssertTrue(menu.waitForExistence(timeout: 10))
        menu.tap()
        app.buttons["Session details"].tap()
        let inbox = app.buttons["Session inbox"]
        XCTAssertTrue(app.navigationBars["Session details"].waitForExistence(timeout: 5))
        for _ in 0..<5 where !inbox.isHittable { app.swipeUp() }
        XCTAssertTrue(inbox.isHittable)
        inbox.tap()
        app.buttons["inbox-session-1"].tap()
        XCTAssertTrue(menu.waitForExistence(timeout: 5))
        XCTAssertFalse(app.navigationBars["Session details"].exists)
        let receipt = app.buttons["latest-instruction"]
        XCTAssertTrue(receipt.waitForExistence(timeout: 5))
        receipt.tap()
        app.buttons["inbox-session-1"].tap()
        XCTAssertTrue(menu.waitForExistence(timeout: 5))
        XCTAssertFalse(app.navigationBars["Session inbox"].exists)
    }

    func testExpiredInstructionsAreOnlyInHistory() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app"]
        app.launch()
        app.tabBars.buttons["Inbox"].tap()
        XCTAssertTrue(app.staticTexts["Review delivery"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.staticTexts["Old release reminder"].exists)
        app.segmentedControls.buttons["History"].tap()
        XCTAssertTrue(app.staticTexts["Old release reminder"].waitForExistence(timeout: 5))
        app.buttons["Send again"].tap()
        XCTAssertTrue(app.sheets["Send this instruction again?"].waitForExistence(timeout: 5))
        if app.buttons["Cancel"].exists { app.buttons["Cancel"].tap() }
        else { app.coordinate(withNormalizedOffset: CGVector(dx: 0.9, dy: 0.8)).tap() }
        XCTAssertTrue(app.staticTexts["Old release reminder"].exists)
    }
}
