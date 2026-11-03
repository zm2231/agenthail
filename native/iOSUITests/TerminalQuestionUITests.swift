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
}
