import XCTest

@MainActor
final class GoalStatusUITests: XCTestCase {
    func testClaudeGoalOffersSetAndClearButNoPauseOrBudget() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-inspector", "--preview-claude-goal"]
        app.launch()
        let details = app.collectionViews.firstMatch
        XCTAssertTrue(details.waitForExistence(timeout: 10))
        let organize = app.buttons["Name conversation"]
        for _ in 0..<4 where !(organize.exists && organize.isHittable) { details.swipeUp() }
        XCTAssertTrue(organize.isHittable)
        XCTAssertTrue(app.buttons["Edit goal"].exists)
        XCTAssertTrue(app.buttons["Clear goal"].exists)
        for control in ["Pause goal", "Resume goal", "Set token budget", "Edit token budget", "Clear token budget"] {
            XCTAssertFalse(app.buttons[control].exists, control)
        }
        let reason = element(app, "The signing certificate is not installed on this machine")
        for _ in 0..<6 where !(reason.exists && reason.isHittable) { details.swipeUp() }
        XCTAssertTrue(reason.isHittable)
        XCTAssertTrue(element(app, "Make the release build pass on a clean checkout").exists)
        XCTAssertTrue(element(app, "Needs you").exists)
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = "Claude goal with set and clear only"
        attachment.lifetime = .keepAlways
        add(attachment)
    }

    private func element(_ app: XCUIApplication, _ text: String) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@ OR value CONTAINS %@", text, text)).firstMatch
    }
}
