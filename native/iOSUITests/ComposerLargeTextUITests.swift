import XCTest

@MainActor
final class ComposerLargeTextUITests: XCTestCase {
    func testTheComposerLeavesMostOfTheScreenToTheTranscriptAtTheLargestTextSize() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-theme", "dark", "-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"]
        app.launch()
        let input = app.descendants(matching: .any).matching(identifier: "composer-input").firstMatch
        XCTAssertTrue(input.waitForExistence(timeout: 10))
        let send = app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Send'")).firstMatch
        XCTAssertTrue(send.waitForExistence(timeout: 10))
        sleep(2)
        let screen = app.windows.firstMatch.frame
        let composerTop = input.frame.minY - 8
        XCTAssertLessThan((screen.maxY - composerTop) / screen.height, 0.4, "the composer takes too much of the screen")
        XCTAssertEqual(app.buttons["composer-model-picker"].frame.midY, send.frame.midY, accuracy: 4, "controls that fit share one row")
    }
}
