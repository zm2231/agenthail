import XCTest

@MainActor
final class TurnNoticeUITests: XCTestCase {
    private func launch(keepingDefaults: Bool = false) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-turn-notice", "--preview-theme", "dark"] + (keepingDefaults ? ["--preview-keep-defaults"] : [])
        app.launch()
        return app
    }

    func testTheTerminalNoteHidesForItsSessionAndStaysHidden() {
        var app = launch()
        let notice = app.otherElements["turn-notice"]
        XCTAssertTrue(notice.waitForExistence(timeout: 15))
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "turn-notice"
        shot.lifetime = .keepAlways
        add(shot)
        app.buttons["turn-notice-dismiss"].tap()
        XCTAssertTrue(notice.waitForNonExistence(timeout: 5))
        XCTAssertTrue(app.otherElements["composer"].exists)

        app.terminate()
        app = launch(keepingDefaults: true)
        XCTAssertTrue(app.otherElements["composer"].waitForExistence(timeout: 15))
        XCTAssertFalse(app.otherElements["turn-notice"].exists, "a dismissed note stays hidden for that session after relaunch")
    }

    func testDontShowAgainHidesTheNote() {
        let app = launch()
        XCTAssertTrue(app.otherElements["turn-notice"].waitForExistence(timeout: 15))
        app.buttons["turn-notice-never"].tap()
        XCTAssertTrue(app.otherElements["turn-notice"].waitForNonExistence(timeout: 5))
    }
}
