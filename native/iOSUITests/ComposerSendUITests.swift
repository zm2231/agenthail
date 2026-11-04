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

    func testALostResponseClearsTheComposerAndCanBePutBack() {
        let app = launch("--preview-send-lost")
        let composer = app.textViews["composer-input"]
        let text = composer.value as? String
        app.buttons["Send message"].tap()
        XCTAssertTrue(app.staticTexts["send-unconfirmed"].waitForExistence(timeout: 5))
        XCTAssertNotEqual(composer.value as? String, text, "the message may have arrived, so it does not stay in the composer")
        attachScreenshot(app, "unconfirmed")
        app.buttons["send-put-back"].tap()
        let confirm = app.sheets.buttons["Put back"].firstMatch.exists ? app.sheets.buttons["Put back"].firstMatch : app.buttons["Put back"].firstMatch
        XCTAssertTrue(confirm.waitForExistence(timeout: 5))
        confirm.tap()
        let restored = NSPredicate(format: "value == %@", text ?? "")
        expectation(for: restored, evaluatedWith: composer)
        waitForExpectations(timeout: 5)
        XCTAssertFalse(app.staticTexts["send-unconfirmed"].exists)
    }
}
