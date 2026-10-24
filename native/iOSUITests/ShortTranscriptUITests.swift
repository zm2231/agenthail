import XCTest

@MainActor
final class ShortTranscriptUITests: XCTestCase {
    func testAShortTranscriptStartsRightBelowTheHeader() throws {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-theme", "dark"]
        app.launch()
        let working = app.staticTexts["Working"]
        XCTAssertTrue(working.waitForExistence(timeout: 10))
        let older = app.buttons["Load older activity"]
        XCTAssertTrue(older.waitForExistence(timeout: 10))
        sleep(2)
        XCTAssertLessThan(older.frame.minY - working.frame.maxY, 60, "a short transcript leaves an empty band under the header")
    }
}
