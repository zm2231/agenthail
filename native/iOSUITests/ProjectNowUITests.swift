import XCTest

@MainActor
final class ProjectNowUITests: XCTestCase {
    private func launch(_ scenario: String, theme: String = "dark") -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-project", "-preview-now", scenario, "--preview-theme", theme]
        app.launch()
        XCTAssertTrue(now(app).waitForExistence(timeout: 15))
        return app
    }

    private func now(_ app: XCUIApplication) -> XCUIElement {
        app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH 'Now'")).firstMatch
    }

    func testNowComesBeforeTheLeadWhenItHasRows() {
        let app = launch("single")
        XCTAssertTrue(app.staticTexts["Lead"].waitForExistence(timeout: 5))
        XCTAssertLessThan(top(now(app)), top(app.staticTexts["Lead"]), "what needs you is the first thing on the screen")
    }

    func testTheLeadStaysFirstWithNothingInNow() {
        let app = launch("empty")
        XCTAssertTrue(app.staticTexts["Lead"].waitForExistence(timeout: 5))
        XCTAssertLessThan(top(app.staticTexts["Lead"]), top(now(app)))
        XCTAssertTrue(app.staticTexts["Nothing in the last day"].exists)
    }

    func testAGroupLineSitsCloseToTheRowsUnderIt() {
        let app = launch("multi")
        let line = app.staticTexts["feat/export"]
        let title = app.staticTexts["Add CSV export"]
        XCTAssertTrue(line.waitForExistence(timeout: 5))
        XCTAssertTrue(title.waitForExistence(timeout: 5))
        XCTAssertLessThan(title.frame.minY - line.frame.maxY, 22, "the line reads as the header of the rows under it")
    }

    private func record(_ app: XCUIApplication, _ name: String) {
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }

    private func top(_ element: XCUIElement) -> CGFloat { element.frame.minY }

    func testSeveralCheckoutsGroupAndSortByWhatNeedsThePerson() {
        let app = launch("multi")
        XCTAssertTrue(app.staticTexts["Now · 5"].exists)
        XCTAssertFalse(app.staticTexts["Recent"].exists)
        XCTAssertFalse(app.staticTexts["Running now · 3"].exists)
        let search = app.staticTexts["fix/search · modified"]
        let export = app.staticTexts["feat/export"]
        let main = app.staticTexts["main"]
        XCTAssertTrue(search.exists)
        XCTAssertLessThan(top(search), top(export))
        XCTAssertLessThan(top(export), top(main))
        let waiting = app.buttons["project-session-computer::waiting"]
        let build = app.buttons["project-session-computer::build"]
        XCTAssertLessThan(top(search), top(waiting))
        XCTAssertLessThan(top(waiting), top(build), "waiting on you sorts above running")
        waiting.tapWhenReady()
        XCTAssertTrue(app.buttons["Session menu"].waitForExistence(timeout: 10), "a row opens its session")
    }

    func testASingleCheckoutShowsNoGroupHeader() {
        let app = launch("single")
        XCTAssertTrue(app.staticTexts["Now · 2"].exists)
        XCTAssertFalse(app.staticTexts["main"].exists)
        XCTAssertLessThan(top(app.buttons["project-session-computer::waiting"]), top(app.buttons["project-session-computer::idle"]))
    }

    func testAnUnreachableComputerSortsLastAndSaysSo() {
        let app = launch("unreachable")
        let far = app.staticTexts["main · build-server · unreachable"]
        XCTAssertTrue(far.waitForExistence(timeout: 5))
        XCTAssertLessThan(top(app.staticTexts["main · studio-mac"]), top(far))
    }

    func testFoldersOutsideARepositoryAreNamedByFolder() {
        let app = launch("plain")
        XCTAssertLessThan(top(app.staticTexts["notes"]), top(app.staticTexts["scans"]))
    }

    func testShots() throws {
        try XCTSkipUnless(ProcessInfo.processInfo.environment["NOW_SHOTS"] == "1", "eight launches, too slow for every run")
        for scenario in ["multi", "single", "unreachable", "plain"] {
            for theme in ["dark", "light"] {
                let app = launch(scenario, theme: theme)
                sleep(1)
                record(app, "\(scenario)-\(theme)")
                app.terminate()
            }
        }
    }
}
