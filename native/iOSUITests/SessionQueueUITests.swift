import XCTest

@MainActor
final class SessionQueueUITests: XCTestCase {
    func testQueuedMessagesShowAboveTheComposerWithTheirAttachmentsAndActions() {
        let app = launch(theme: "dark")
        XCTAssertTrue(app.buttons["session-queue-header"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["This is the failing screen, match the spacing."].exists)
        XCTAssertTrue(app.descendants(matching: .any)["session-queue-attachments-11"].exists)
        XCTAssertTrue(app.buttons["session-queue-steer-11"].isEnabled)
        XCTAssertTrue(app.buttons["session-queue-edit-11"].exists)
        XCTAssertTrue(app.buttons["session-queue-cancel-11"].exists)
        XCTAssertFalse(app.buttons["session-queue-steer-12"].isEnabled, "steer can't carry the row's model")
        XCTAssertTrue(app.staticTexts["session-queue-refusal-12"].exists)
        attach(app, "Queued rows, dark")
        app.staticTexts["session-queue-refusal-12"].swipeUp()
        attach(app, "Queued rows scrolled to a row that can't steer, dark")
    }

    func testQueuedRowsReadInLightMode() {
        let app = launch(theme: "light")
        XCTAssertTrue(app.buttons["session-queue-header"].waitForExistence(timeout: 10))
        attach(app, "Queued rows, light")
    }

    private func launch(theme: String) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-session-queue", "--preview-theme", theme]
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
