import XCTest

@MainActor
final class InboxRowHitAreaUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    func testTheEmptyPartOfACurrentAndAHistoryRowOpensItsSession() {
        let app = launchInbox()
        assertOpensFromTheGap(app.buttons["inbox-session-2"], in: app)
        app.tab("Inbox").tapWhenReady()
        app.buttons["History"].tapWhenReady()
        assertOpensFromTheGap(app.buttons["inbox-message-13"], in: app)
    }

    private func launchInbox() -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-delivery"]
        app.launch()
        app.tab("Inbox").tapWhenReady(timeout: 15)
        return app
    }

    private func assertOpensFromTheGap(_ row: XCUIElement, in app: XCUIApplication, file: StaticString = #filePath, line: UInt = #line) {
        XCTAssertTrue(row.waitForExistence(timeout: 10), file: file, line: line)
        let frame = row.frame
        row.coordinate(withNormalizedOffset: CGVector(dx: 0.75, dy: 0.5)).tap()
        XCTAssertTrue(app.buttons["Session menu"].waitForExistence(timeout: 10), "a tap between the title and the chevron opens the session", file: file, line: line)
        app.backToSessions(file: file, line: line)
        XCTAssertTrue(app.tab("Sessions").waitForExistence(timeout: 5) && app.tab("Sessions").isSelected, "the session opened in the Sessions tab", file: file, line: line)
        XCTAssertGreaterThanOrEqual(frame.height, 44, "the row answers taps across its whole 44 pt height", file: file, line: line)
    }
}
