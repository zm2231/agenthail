import XCTest

@MainActor
final class NewSessionAccountUITests: XCTestCase {
    func testAutomaticIsTheFirstAccountAndSaysWhatItPicksAndWhy() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-new"] + (ProcessInfo.processInfo.environment["AGENTHAIL_UI_THEME"].map { ["--preview-theme", $0] } ?? [])
        app.launch()
        let reason = app.staticTexts["Automatic picks team: most headroom, session 12%, week 30%"]
        XCTAssertTrue(reason.waitForExistence(timeout: 10), "the reason for the automatic pick shows under the account picker")
        let picker = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "Automatic (team, most headroom)")).firstMatch
        XCTAssertTrue(picker.exists, "the picker shows Automatic and the account it picks")
        let closed = XCTAttachment(screenshot: app.screenshot())
        closed.name = "new session automatic account"
        closed.lifetime = .keepAlways
        add(closed)
        picker.tap()
        XCTAssertTrue(app.buttons["demo · session 40%, week 93%, over limit"].waitForExistence(timeout: 5), "each account shows its usage, and one over a ceiling says so")
        XCTAssertTrue(app.buttons["shared via proxy · next: ops · session 18%, week 41%"].exists, "a root through a proxy reads as the account the proxy would use next")
        let open = XCTAttachment(screenshot: app.screenshot())
        open.name = "new session account menu"
        open.lifetime = .keepAlways
        add(open)
    }
}
