import XCTest

@MainActor
final class SessionMenuTurnSettingsUITests: XCTestCase {
    func testSessionDetailsPermissionModeNamesItsValueAndOffersTheQuietModes() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-theme", "dark"]
        app.launch()
        let menu = app.buttons["Session menu"]
        XCTAssertTrue(menu.waitForExistence(timeout: 10))
        menu.tapWhenReady()
        app.buttons["Session details"].tapWhenReady()
        XCTAssertTrue(app.navigationBars["Session details"].waitForExistence(timeout: 5))
        let mode = app.buttons["session-permission-mode"]
        let list = app.collectionViews.firstMatch
        for _ in 0..<6 where !mode.isHittable { list.swipeUp() }
        XCTAssertTrue(mode.isHittable, "session details offer the permission mode")
        XCTAssertEqual(mode.label, "Mode, Accept edits", "the session's permission mode names its setting and value as the composer chip does")
        mode.tap()
        XCTAssertTrue(app.buttons["Plan"].waitForExistence(timeout: 5), "the menu offers the other modes")
        XCTAssertTrue(app.buttons["Accept edits"].isSelected, "the menu marks the current mode as the composer's picker does")
        XCTAssertFalse(app.buttons["Bypass permissions"].exists, "the iPhone never offers a mode that lets the agent act without asking")
    }

    func testTheHeaderOpensTheModelAndEffortInOnePlace() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-theme", "dark"]
        app.launch()
        let summary = app.buttons["session-summary"]
        XCTAssertTrue(summary.waitForExistence(timeout: 10))
        summary.tapWhenReady()
        XCTAssertTrue(app.navigationBars["Session details"].waitForExistence(timeout: 5))
        let effort = app.buttons["session-effort"]
        let list = app.collectionViews.firstMatch
        for _ in 0..<6 where !effort.isHittable { list.swipeUp() }
        XCTAssertTrue(app.buttons["Change model"].exists, "session details change the model")
        XCTAssertTrue(effort.isHittable, "session details change the effort")
        XCTAssertEqual(effort.label, "Effort, High", "the effort names its setting and value")
        XCTAssertFalse(app.buttons["Change effort"].exists, "the effort is offered once")
        effort.tap()
        XCTAssertTrue(app.buttons["Low"].waitForExistence(timeout: 5), "the menu offers the other efforts")
        XCTAssertTrue(app.buttons["High"].isSelected, "the menu marks the current effort")
    }
}
