import XCTest

@MainActor
final class TurnSettingChipsUITests: XCTestCase {
    func testTheComposerOffersOnlyTheModeAndItsMenuOffersTheQuietModes() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-theme", "dark"]
        app.launch()
        let mode = app.buttons.matching(NSPredicate(format: "identifier IN %@", ["composer-mode", "composer-turn-more"])).firstMatch
        XCTAssertTrue(mode.waitForExistence(timeout: 10), "the composer keeps the mode")
        XCTAssertEqual(mode.label, "Mode, Accept edits", "the mode chip names its setting and value")
        XCTAssertFalse(app.buttons["Model, Claude Sonnet"].exists, "the model lives in the session header, not the composer")
        XCTAssertFalse(app.buttons["Effort, High"].exists, "the effort lives in session details, not the composer")
        mode.tap()
        XCTAssertTrue(app.buttons["Plan"].waitForExistence(timeout: 5), "the mode menu offers the other modes")
        XCTAssertFalse(app.buttons["Bypass permissions"].exists, "the iPhone never offers a mode that lets the agent act without asking")
    }
}
