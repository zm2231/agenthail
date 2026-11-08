import XCTest

@MainActor
final class TurnSettingChipsUITests: XCTestCase {
    func testEachChipNamesItsSettingAndValueAndItsMenuOffersTheValues() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-theme", "dark"]
        app.launch()
        let model = app.buttons["Model, Claude Sonnet"]
        XCTAssertTrue(model.waitForExistence(timeout: 10), "the model chip names its setting and value")
        XCTAssertTrue(app.buttons["Effort, High"].exists, "the effort chip names its setting and value")
        let mode = app.buttons["Mode, Accept edits"]
        XCTAssertTrue(mode.exists, "the mode chip names its setting and value")
        mode.tap()
        XCTAssertTrue(app.buttons["Plan"].waitForExistence(timeout: 5), "the mode menu offers the other modes")
        XCTAssertFalse(app.buttons["Bypass permissions"].exists, "the iPhone never offers a mode that lets the agent act without asking")
    }
}
