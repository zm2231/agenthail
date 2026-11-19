import XCTest

final class HappeningNowUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    private func launch(focus: Bool) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-theme", "dark", "--preview-reading", "--preview-crew", "-transcriptFocus", focus ? "YES" : "NO"]
        app.launch()
        return app
    }

    private func keep(_ app: XCUIApplication, _ name: String) {
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }

    func testALeadOpensItsWorkersFromTheConversation() {
        let app = launch(focus: false)
        let now = app.buttons["happening-now"]
        XCTAssertTrue(now.waitForExistence(timeout: 30), "a session with workers offers its Now sheet")
        keep(app, "A lead's conversation with its Now button")
        now.tap()
        let working = app.buttons["crew-computer::crew-check"]
        let idle = app.buttons["crew-computer::crew-cleanup"]
        XCTAssertTrue(working.waitForExistence(timeout: 10))
        XCTAssertTrue(idle.exists)
        XCTAssertLessThan(working.frame.minY, idle.frame.minY, "the working worker comes before the idle one")
        let label = app.staticTexts["Said it would continue"]
        XCTAssertTrue(label.exists, "an idle worker that will continue says so")
        XCTAssertGreaterThan(label.frame.width, 120, "the state label keeps its width beside a long name: \(label.frame)")
        XCTAssertTrue(app.staticTexts["@faa-cleanup"].exists, "a card names the worker, not the lead it belongs to")
        keep(app, "The Now sheet with a working and an idle worker")
    }

    func testFocusShowsTheTurnsWorkAsOneLine() {
        let app = launch(focus: true)
        XCTAssertTrue(app.buttons["happening-now"].waitForExistence(timeout: 30))
        let line = app.buttons.matching(NSPredicate(format: "label CONTAINS[c] %@", "of work")).firstMatch
        XCTAssertTrue(line.waitForExistence(timeout: 10), "the turn's work folds into one line")
        keep(app, "Focus with the turn's work in one line")
        line.tap()
        keep(app, "Focus with the turn's work opened")
    }
}
