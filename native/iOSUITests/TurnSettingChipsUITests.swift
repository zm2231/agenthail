import XCTest

@MainActor
final class TurnSettingChipsUITests: XCTestCase {
    func testEachChipNamesItsSettingAndValueAndItsMenuOffersTheValues() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-theme", "dark"]
        app.launch()
        let model = app.buttons["Model, Claude Sonnet"]
        XCTAssertTrue(model.waitForExistence(timeout: 10), "the model chip names its setting and value")
        let more = app.buttons["composer-turn-more"]
        let folded = more.exists ? more.label : ""
        XCTAssertTrue(app.buttons["Effort, High"].exists || folded.contains("Effort, High"), "the effort setting names its value, as a chip or in the menu it folds into")
        let mode = app.buttons["Mode, Accept edits"]
        XCTAssertTrue(mode.exists || folded.contains("Mode, Accept edits"), "the mode setting names its value, as a chip or in the menu it folds into")
        (mode.exists ? mode : more).tap()
        XCTAssertTrue(app.buttons["Plan"].waitForExistence(timeout: 5), "the mode menu offers the other modes")
        XCTAssertFalse(app.buttons["Bypass permissions"].exists, "the iPhone never offers a mode that lets the agent act without asking")
    }
}
