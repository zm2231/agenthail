import XCTest

@MainActor
final class NewSessionAccountUITests: XCTestCase {
    func testAutomaticIsTheFirstAccountAndSaysWhatItPicksAndWhy() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-new"] + (ProcessInfo.processInfo.environment["AGENTHAIL_UI_THEME"].map { ["--preview-theme", $0] } ?? [])
        app.launch()
        let reason = app.staticTexts["Automatic · team (team@example.com): most headroom, session 12%, week 30%"]
        XCTAssertTrue(reason.waitForExistence(timeout: 10), "the reason for the automatic pick shows under the account picker")
        XCTAssertTrue(app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "Automatic · team")).firstMatch.exists, "the picker shows Automatic and the account it picks")
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "new session automatic account"
        shot.lifetime = .keepAlways
        add(shot)
    }
}
