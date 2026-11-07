import XCTest

@MainActor
final class ComposerSendUITests: XCTestCase {
    private func launch(_ argument: String) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", argument, "--preview-theme", "dark"]
        app.launch()
        XCTAssertTrue(app.textViews["composer-input"].waitForExistence(timeout: 10))
        return app
    }

    private func attachScreenshot(_ app: XCUIApplication, _ name: String) {
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }

    func testARefusedSendShowsWhyWithRetryAndKeepsTheText() {
        let app = launch("--preview-send-refused")
        let composer = app.textViews["composer-input"]
        let text = composer.value as? String
        app.buttons["Send message"].tap()
        XCTAssertTrue(app.staticTexts["send-failure"].waitForExistence(timeout: 5))
        XCTAssertEqual(app.staticTexts["send-failure"].label, "This session has ended.")
        XCTAssertTrue(app.buttons["send-retry"].exists)
        XCTAssertEqual(composer.value as? String, text)
        attachScreenshot(app, "refused")
        app.buttons["send-retry"].tap()
        XCTAssertTrue(app.staticTexts["send-failure"].waitForExistence(timeout: 5))
    }
}
