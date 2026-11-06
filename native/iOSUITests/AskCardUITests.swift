import XCTest

@MainActor
final class AskCardUITests: XCTestCase {
    func testAnswersAnAskWithANoteInDark() {
        answer(theme: "dark")
    }

    func testAnswersAnAskWithANoteInLight() {
        answer(theme: "light")
    }

    private func answer(theme: String) {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-ask", "--preview-theme", theme]
        app.launch()
        let heading = app.staticTexts["@builder asks: Ready to ship"]
        XCTAssertTrue(heading.waitForExistence(timeout: 10), "the ask shows with the session's name though it answers nothing through its provider")
        XCTAssertTrue(app.staticTexts["· Suggested"].exists || app.buttons.matching(NSPredicate(format: "label CONTAINS 'Suggested'")).firstMatch.exists, "the suggested option is marked")
        XCTAssertTrue(app.buttons["Runbook"].exists || app.links["Runbook"].exists, "the ask's link shows")
        XCTAssertFalse(app.buttons["Decline"].exists, "an ask has nothing to decline")
        let submit = app.buttons["Submit"]
        XCTAssertFalse(submit.isEnabled, "nothing is answered yet")
        attach(app, "\(theme) ask")

        app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Wait'")).firstMatch.tap()
        XCTAssertTrue(heading.exists && submit.isEnabled, "a pick waits for Submit")

        let note = app.textFields["question-pager-note"].exists ? app.textFields["question-pager-note"] : app.textViews["question-pager-note"]
        note.tap()
        note.typeText("After lunch.")
        attach(app, "\(theme) ask answered")
        submit.tap()

        XCTAssertTrue(heading.waitForNonExistence(timeout: 10), "the answered ask goes and is not offered again")
        let receipt = app.descendants(matching: .any)["ask-answer-receipt"]
        XCTAssertTrue(receipt.waitForExistence(timeout: 5), "a receipt says the answer went")
        XCTAssertTrue(receipt.label.contains("You answered: Wait · After lunch."), "the receipt names the answer: \(receipt.label)")
        XCTAssertFalse(app.staticTexts.matching(NSPredicate(format: "label CONTAINS 'unexpected answer'")).firstMatch.exists, "the daemon got the contract's payload")
        attach(app, "\(theme) after answer")
    }

    private func attach(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
