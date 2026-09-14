import XCTest

@MainActor
final class TranscriptReadingUITests: XCTestCase {
    func testLongSetupDoesNotBuryTheLatestWork() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-reading"]
        app.launch()
        let latest = app.staticTexts["The latest update is visible above the composer."]
        XCTAssertTrue(latest.waitForExistence(timeout: 10))
        XCTAssertTrue(latest.isHittable)
        let composer = app.descendants(matching: .any).matching(identifier: "composer-input").firstMatch
        XCTAssertLessThanOrEqual(latest.frame.maxY, composer.frame.minY)
        capture("Reading at latest work", app)

        let context = app.buttons["context-context"]
        XCTAssertTrue(context.waitForExistence(timeout: 5))
        for _ in 0..<8 { app.swipeDown() }
        XCTAssertFalse(app.staticTexts["Repository guidance"].isHittable)
        capture("Context and long message collapsed", app)
        context.tap()
        XCTAssertTrue(app.navigationBars["Repository instructions"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["Copy text"].exists)
        app.buttons["Done"].tap()
        let fullMessage = app.buttons["Read full message"]
        XCTAssertTrue(fullMessage.waitForExistence(timeout: 5))
        fullMessage.tap()
        XCTAssertTrue(app.navigationBars["Your message"].waitForExistence(timeout: 5))
        app.buttons["Done"].tap()
        let jump = app.buttons["Jump to latest"]
        XCTAssertTrue(jump.isHittable)
        jump.tap()
        XCTAssertTrue(latest.isHittable)
    }

    private func capture(_ name: String, _ app: XCUIApplication) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
