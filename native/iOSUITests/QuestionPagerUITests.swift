import XCTest

@MainActor
final class QuestionPagerUITests: XCTestCase {
    func testTwoQuestionsPageThroughToAReviewInDark() {
        pageThrough(theme: "dark")
    }

    func testTwoQuestionsPageThroughToAReviewInLight() {
        pageThrough(theme: "light")
    }

    private func pageThrough(theme: String) {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-questions", "--preview-theme", theme]
        app.launch()
        let first = app.buttons["Stream reader first, Smaller change that unblocks the failing regression test today"]
        XCTAssertTrue(first.waitForExistence(timeout: 10))
        XCTAssertFalse(app.staticTexts["Where should the regression check run?"].exists, "only the current question shows")
        let next = app.buttons["Next"]
        let decline = app.buttons["Decline"]
        XCTAssertTrue(next.isHittable && decline.isHittable, "the actions stay on screen under four described options")
        attach(app, "\(theme) question 1")

        first.tap()
        let second = app.staticTexts["Where should the regression check run?"]
        XCTAssertTrue(second.waitForExistence(timeout: 5), "a single-select pick advances")
        XCTAssertEqual(app.buttons.matching(identifier: "question-pager-chips").element(boundBy: 0).value as? String, "Answered")
        app.buttons["Mac, Run the suite on the desktop build"].tap()
        app.buttons["Dashboard, Run it in the browser harness"].tap()
        XCTAssertTrue(second.exists, "a multi-select toggle stays on its page")
        attach(app, "\(theme) question 2")

        next.tap()
        XCTAssertTrue(app.staticTexts["Review your answers"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["Submit answers"].isEnabled)
        XCTAssertTrue(app.buttons["Checks, Mac, Dashboard"].exists, "review lists header and answer")
        XCTAssertTrue(decline.isHittable)
        attach(app, "\(theme) review")

        app.buttons.matching(identifier: "question-pager-chips").element(boundBy: 0).tap()
        XCTAssertTrue(app.buttons["Stream reader first, Smaller change that unblocks the failing regression test today"].waitForExistence(timeout: 5), "a chip goes back to its question")

        let other = app.textFields["Other answer"]
        other.tap()
        other.typeText("Write the test first")
        XCTAssertEqual(other.value as? String, "Write the test first", "the Other field takes typing inside the card")
        app.buttons.matching(identifier: "question-pager-chips").element(boundBy: 2).tap()
        XCTAssertTrue(app.buttons["Landing order for the reconnect fix, Write the test first"].waitForExistence(timeout: 5), "a typed answer replaces the pick in the review")
    }

    private func attach(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
