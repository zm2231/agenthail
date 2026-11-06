import XCTest

@MainActor
final class InboxSteerUITests: XCTestCase {
    private func openInbox(theme: String) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-steer-rows", "--preview-theme", theme]
        app.launch()
        app.tab("Inbox").tapWhenReady(timeout: 15)
        return app
    }

    private func record(_ app: XCUIApplication, _ name: String) {
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }

    func testAWaitingRowOffersSteerAndARowThatCantSteerSaysWhy() {
        let app = openInbox(theme: "dark")
        let steer = app.buttons["queue-steer-11"]
        XCTAssertTrue(steer.waitForExistence(timeout: 10), "a person's image row on a running session offers Steer")
        XCTAssertTrue(steer.isHittable)
        XCTAssertGreaterThanOrEqual(steer.frame.height, 44)
        record(app, "inbox-steer-dark")
        let refused = app.staticTexts["queue-steer-refusal-12"]
        for _ in 0..<4 where !refused.exists { app.swipeUp() }
        XCTAssertTrue(refused.exists, "a row with a model says why it can't steer")
        XCTAssertFalse(app.buttons["queue-steer-12"].exists)
        record(app, "inbox-steer-refused-dark")
    }

    func testTappingSteerSendsTheRowAsASteerAndItLeaves() {
        let app = openInbox(theme: "dark")
        let steer = app.buttons["queue-steer-11"]
        XCTAssertTrue(steer.waitForExistence(timeout: 10))
        steer.tap()
        XCTAssertTrue(steer.waitForNonExistence(timeout: 5), "the row stays until a send with steer delivery arrives")
        XCTAssertTrue(app.staticTexts["queue-steer-refusal-12"].exists, "the other row stays")
    }

    func testSteerReadsInLightMode() {
        let app = openInbox(theme: "light")
        XCTAssertTrue(app.buttons["queue-steer-11"].waitForExistence(timeout: 10))
        record(app, "inbox-steer-light")
    }
}
