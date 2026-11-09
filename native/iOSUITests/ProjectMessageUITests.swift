import XCTest

@MainActor
final class ProjectMessageUITests: XCTestCase {
    func testAProjectIsMessagedFromItsScreenAndSaysWhoGotIt() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-project-message", "--preview-theme", "dark"]
        app.launch()
        let open = app.buttons["open-project-computer::project-fieldnotes"]
        XCTAssertTrue(open.waitForExistence(timeout: 10))
        open.tapWhenReady()
        let message = app.buttons["project-message"]
        for _ in 0..<3 where !message.isHittable { app.swipeUp() }
        message.tapWhenReady()
        let field = app.textFields["project-message-text"].exists ? app.textFields["project-message-text"] : app.textViews["project-message-text"]
        XCTAssertTrue(field.waitForExistence(timeout: 5))
        field.tap()
        field.typeText("Ship the field notes today.")
        app.buttons["project-message-send"].tapWhenReady()
        let sent = app.staticTexts["project-message-sent"]
        XCTAssertTrue(sent.waitForExistence(timeout: 5))
        XCTAssertTrue(sent.label.hasPrefix("Sent to 1:"), sent.label)
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "project message sent"
        shot.lifetime = .keepAlways
        add(shot)
        app.buttons["Done"].tapWhenReady()
        XCTAssertFalse(sent.waitForExistence(timeout: 2))
    }
}
