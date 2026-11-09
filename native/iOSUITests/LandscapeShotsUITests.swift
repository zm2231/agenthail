import XCTest

@MainActor
final class LandscapeShotsUITests: XCTestCase {
    func testLandscapeShotsForReview() {
        XCUIDevice.shared.orientation = .landscapeLeft
        addTeardownBlock { @MainActor in XCUIDevice.shared.orientation = .portrait }
        for theme in ["dark", "light"] {
            let app = XCUIApplication()
            app.launchArguments = ["--preview-session", "--preview-reading", "--preview-tail", "-preview-tail", "review", "--preview-theme", theme]
            app.launch()
            XCTAssertTrue(app.descendants(matching: .any)["queue-computer/26"].waitForExistence(timeout: 10))
            XCTAssertTrue(app.scrollViews["session-timeline"].waitUntilRowsStill())
            shot("\(theme) landscape at the latest row")
            app.buttons["queue-tail-read-26"].tap()
            _ = app.descendants(matching: .any)["queued-message-reason"].waitForExistence(timeout: 5)
            shot("\(theme) landscape delivery sheet")
            app.buttons["Done"].tap()
            app.descendants(matching: .any)["composer-turn-settings"].tap()
            RunLoop.current.run(until: Date().addingTimeInterval(1))
            shot("\(theme) landscape settings menu")
            app.terminate()
        }
        XCTFail("deliberate failure so CI keeps the screenshots")
    }

    private func shot(_ name: String) {
        let attachment = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
