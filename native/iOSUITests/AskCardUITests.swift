import XCTest

@MainActor
final class AskCardUITests: XCTestCase {
    func testAnswersAnAskWithANoteInDark() {
        answer(theme: "dark")
    }

    func testAnswersAnAskWithANoteInLight() {
        answer(theme: "light")
    }

    func testAcceptsTheRecommendedAnswerFromTheInbox() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-ask", "--preview-theme", "dark"]
        app.launch()
        app.tab("Inbox").tapWhenReady(timeout: 15)
        let decision = app.buttons.matching(NSPredicate(format: "identifier ENDSWITH %@", "demo")).matching(NSPredicate(format: "identifier BEGINSWITH 'decision-'")).firstMatch
        XCTAssertTrue(decision.waitForExistence(timeout: 10), "the open ask is listed in the inbox")
        decision.tap()
        let accept = app.buttons["decision-accept-recommended"]
        XCTAssertTrue(accept.waitForExistence(timeout: 5), "an ask that names a recommendation offers to accept it")
        attach(app, "dark accept recommended")
        accept.tap()
        XCTAssertTrue(accept.waitForNonExistence(timeout: 10), "accepting answers the ask and closes it")
        XCTAssertFalse(app.descendants(matching: .any)["decision-failure"].exists, "the daemon got the recommended option alone")
        XCTAssertTrue(decision.waitForNonExistence(timeout: 10), "the answered ask leaves the inbox")
        attach(app, "dark after accept")
    }

    func testANewerAskShowsWithoutAnsweringTheOlderOne() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-ask", "--preview-two-asks", "--preview-theme", "light"]
        app.launch()
        let first = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "@builder asks: Ready to ship")).firstMatch
        XCTAssertTrue(first.waitForExistence(timeout: 10), "the older ask shows first")
        let next = app.buttons["request-next"]
        XCTAssertTrue(next.waitForExistence(timeout: 5) && next.isEnabled, "a second open ask can be reached")
        XCTAssertTrue(app.staticTexts["1 of 2 waiting on you"].exists, "the bar counts both asks")
        attach(app, "light first of two asks")
        next.tap()
        let second = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "@builder asks: Sign in to the store")).firstMatch
        XCTAssertTrue(second.waitForExistence(timeout: 5), "the newer ask shows without answering the older one")
        XCTAssertTrue(app.staticTexts["2 of 2 waiting on you"].exists && !app.buttons["request-next"].isEnabled, "the newer ask is the last")
        attach(app, "light second of two asks")
        app.buttons["request-previous"].tap()
        XCTAssertTrue(first.waitForExistence(timeout: 5), "the older ask is still there")
    }

    func testAClippedApprovalShowsItsWholeTextWhenOpened() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-ask", "--preview-clipped-approval", "--preview-theme", "dark"]
        app.launch()
        let heading = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "@builder asks: Make its project's coordinator")).firstMatch
        XCTAssertTrue(heading.waitForExistence(timeout: 10), "the clipped approval shows")
        let detail = app.staticTexts["pager-detail"]
        XCTAssertTrue(detail.waitForExistence(timeout: 5), "the opened approval shows its whole text")
        XCTAssertEqual(detail.label, "Make its project's coordinator as @release-train-coordinator-for-the-fieldnotes-search-and-ranking-overhaul, renaming it from @fieldnotes-search-indexing-and-ranking-overhaul-lead-for-the-winter-release, the session demo. Why: the release needs one lead", "both names show whole")
        XCTAssertTrue(app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Allow once'")).firstMatch.exists, "the opened approval can be answered")
        attach(app, "dark clipped approval")
    }

    private func answer(theme: String) {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-ask", "--preview-theme", theme]
        app.launch()
        let heading = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "@builder asks: Ready to ship")).firstMatch
        XCTAssertTrue(heading.waitForExistence(timeout: 10), "the ask shows with the session's name though it answers nothing through its provider")
        XCTAssertTrue(app.staticTexts["· Recommended"].exists || app.buttons.matching(NSPredicate(format: "label CONTAINS 'Recommended'")).firstMatch.exists, "the suggested option is marked Recommended")
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
