import XCTest

@MainActor
final class LoadOlderUITests: XCTestCase {
    func testLoadingOlderActivityKeepsTheReaderOnTheRowTheyWereReading() {
        loadOlderKeepsPlace([])
    }

    func testASlowOlderPageStillKeepsTheReaderOnTheirRow() {
        loadOlderKeepsPlace(["--preview-older-slow"])
    }

    func testJumpingToLatestWhileOlderActivityLoadsStaysAtLatest() {
        let app = launch(["--preview-older-slow"])
        let older = scrollToLoadOlder(app)
        older.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
        let jump = app.buttons["Jump to latest"]
        XCTAssertTrue(jump.waitForExistence(timeout: 2), "Jump to latest is not offered while reading older rows")
        jump.tap()
        sleep(1)
        shot(app, "jumped")
        sleep(4)
        shot(app, "after-jump")
        XCTAssertFalse(jump.exists, "the older page pulled the reader away from the latest activity after Jump to latest")
        XCTAssertFalse(message("Recent message 1.", in: app).isHittable, "the reader was returned to the held row after Jump to latest")
    }

    private func loadOlderKeepsPlace(_ extra: [String]) {
        let app = launch(extra)
        let older = scrollToLoadOlder(app)
        let anchor = message("Recent message 1.", in: app)
        XCTAssertTrue(anchor.exists, "the first recent message is not built at the top")
        let before = anchor.frame.minY
        shot(app, "before")
        older.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
        XCTAssertTrue(message("Earlier message 40.", in: app).waitForExistence(timeout: 10), "the older page did not load")
        XCTAssertTrue(anchor.waitUntilStill(), "the row the reader was on kept moving")
        shot(app, "after")
        XCTAssertTrue(anchor.exists && anchor.isHittable, "the row the reader was on left the screen after loading older activity")
        XCTAssertLessThan(abs(anchor.frame.minY - before), 8, "the row the reader was on moved from \(before) to \(anchor.frame.minY)")
        XCTAssertFalse(message("Earlier message 1.", in: app).isHittable, "loading older activity jumped to the oldest row")
    }

    private func launch(_ extra: [String]) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-theme", "dark", "--preview-older", "--preview-follow"] + extra
        app.launch()
        XCTAssertTrue(app.scrollViews["session-timeline"].waitForExistence(timeout: 15))
        return app
    }

    private func scrollToLoadOlder(_ app: XCUIApplication) -> XCUIElement {
        let timeline = app.scrollViews["session-timeline"]
        let older = app.buttons["Load older activity"]
        let header = app.staticTexts["Working"]
        XCTAssertTrue(header.waitForExistence(timeout: 10))
        for _ in 0..<24 where !(older.exists && older.frame.minY > header.frame.maxY + 8) { timeline.swipeDown(velocity: .slow) }
        XCTAssertTrue(older.waitUntilStill(), "Load older activity kept moving")
        XCTAssertTrue(older.exists && older.frame.minY > header.frame.maxY + 8, "Load older activity is not on screen below the header")
        return older
    }

    private func message(_ prefix: String, in app: XCUIApplication) -> XCUIElement {
        app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", prefix)).firstMatch
    }

    private func shot(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
