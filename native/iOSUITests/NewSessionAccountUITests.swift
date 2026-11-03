import XCTest

@MainActor
final class NewSessionAccountUITests: XCTestCase {
    func testDefaultIsTheFirstAccountAndSaysWhereItGoesAndWhy() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-new"] + (ProcessInfo.processInfo.environment["AGENTHAIL_UI_THEME"].map { ["--preview-theme", $0] } ?? [])
        app.launch()
        let reason = app.staticTexts["Default is team: your default root for new sessions"]
        XCTAssertTrue(reason.waitForExistence(timeout: 10), "the reason for the default shows under the account picker")
        let picker = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "Default (team, your default root for new sessions)")).firstMatch
        XCTAssertTrue(picker.exists, "the picker shows Default and the account it goes to")
        let closed = XCTAttachment(screenshot: app.screenshot())
        closed.name = "new session default account"
        closed.lifetime = .keepAlways
        add(closed)
        picker.tap()
        XCTAssertTrue(app.buttons["demo · session 40%, week 93%"].waitForExistence(timeout: 5), "each account shows its usage")
        XCTAssertTrue(app.buttons["shared via proxy · next: ops · session 18%, week 41%"].exists, "a root through a proxy reads as the account the proxy would use next")
        let open = XCTAttachment(screenshot: app.screenshot())
        open.name = "new session account menu"
        open.lifetime = .keepAlways
        add(open)
        app.buttons["demo · session 40%, week 93%"].tap()
        XCTAssertTrue(app.buttons["creation-default-root"].waitForExistence(timeout: 5), "a chosen root other than the default can become the default")
    }
}
