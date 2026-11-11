import XCTest

@MainActor
final class TerminalQuestionUITests: XCTestCase {
    func testAHeldTerminalQuestionShowsItsCard() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-terminal-question", "--preview-theme", "dark"]
        app.launch()

        let release = app.buttons["Release, Matches what ships"]
        XCTAssertTrue(release.waitForExistence(timeout: 10), "the held question shows its card with its options")
        XCTAssertTrue(app.buttons["Debug, Fast to build, matches local runs"].exists)
        XCTAssertTrue(app.buttons["Decline"].isHittable, "the card can be declined")
        XCTAssertFalse(app.staticTexts.containing(NSPredicate(format: "label CONTAINS[c] %@", "can't answer")).firstMatch.exists, "the question is not shown as one only the terminal can answer")
        XCTAssertFalse(app.descendants(matching: .any).containing(NSPredicate(format: "label CONTAINS %@", #"{"questions""#)).firstMatch.exists, "the pending call does not read as raw JSON")
        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", "Ask you")).firstMatch.exists, "the pending call reads as a question to you")

        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = "held terminal question"
        attachment.lifetime = .keepAlways
        add(attachment)
    }

    func testAHeldQuestionOpenedFromTheListAfterAnotherSessionShowsItsCard() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-terminal-question", "--preview-theme", "dark"]
        app.launch()

        let other = app.buttons["session-computer::codex-demo"]
        XCTAssertTrue(other.waitForExistence(timeout: 10))
        other.tap()
        let back = app.navigationBars.buttons.element(boundBy: 0)
        XCTAssertTrue(back.waitForExistence(timeout: 10))
        back.tap()

        let asking = app.buttons["needs-you-computer::demo"]
        XCTAssertTrue(asking.waitForExistence(timeout: 10))
        asking.tap()
        XCTAssertTrue(app.buttons["Release, Matches what ships"].waitForExistence(timeout: 10), "a question opened from the list after another session shows its card")
        XCTAssertTrue(app.buttons["Decline"].isHittable)

        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = "held question opened from the list"
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
