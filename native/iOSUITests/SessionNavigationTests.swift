import XCTest

@MainActor
final class SessionNavigationTests: XCTestCase {
    func testVoiceEntryPreservesTabNavigation() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-voice-entry"]
        app.launch()
        XCTAssertTrue(app.buttons["voice-entry"].waitForExistence(timeout: 10))
        let workspace = app.buttons["project-project-fieldnotes"]
        XCTAssertTrue(workspace.waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["session-codex-demo"].waitForExistence(timeout: 5))
        workspace.tap()
        XCTAssertFalse(app.buttons["session-codex-demo"].exists)
        XCTAssertTrue(app.buttons["session-demo"].exists, "a collapsed project keeps its working session")
        workspace.tap()
        XCTAssertTrue(app.buttons["session-codex-demo"].waitForExistence(timeout: 5))
        tap(app.buttons["voice-entry"])
        XCTAssertTrue(app.navigationBars["Talk to orchestrator"].waitForExistence(timeout: 5))
        tap(app.buttons["Done"])
        for tab in ["Sessions", "Inbox", "Settings"] {
            XCTAssertTrue(app.tabBars.buttons[tab].isHittable, "\(tab) tab is covered by the Voice entry")
        }
        app.tabBars.buttons["Inbox"].tap()
        XCTAssertTrue(app.tabBars.buttons["Inbox"].isSelected)
        app.tabBars.buttons["Settings"].tap()
        XCTAssertTrue(app.tabBars.buttons["Settings"].isSelected)
        XCTAssertFalse(app.buttons["voice-entry"].exists)
    }

    func testSessionAndInboxNavigationReturnToTheirOrigin() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-delivery"]
        app.launch()
        let session = app.buttons["session-demo"]
        XCTAssertTrue(session.waitForExistence(timeout: 10))
        session.tap()
        XCTAssertFalse(app.buttons["voice-entry"].exists)
        XCTAssertFalse(app.staticTexts["Working on your Mac"].exists)
        XCTAssertTrue(app.buttons["Session menu"].waitForExistence(timeout: 10))
        app.buttons["Session menu"].tap()
        let allEvents = app.buttons["All events"]
        let call = app.buttons["Call this session"]
        XCTAssertTrue(allEvents.waitForExistence(timeout: 5))
        XCTAssertTrue(call.waitForExistence(timeout: 5))
        XCTAssertLessThan(allEvents.frame.maxY, call.frame.minY)
        tap(app.buttons["Chat"])
        XCTAssertTrue(app.buttons["Stop current turn"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.tabBars.buttons["Inbox"].isHittable)
        tap(app.navigationBars.buttons.element(boundBy: 0))
        XCTAssertTrue(session.waitForExistence(timeout: 5))
        tap(app.buttons["new-session"])
        XCTAssertTrue(app.navigationBars["New session"].waitForExistence(timeout: 5))
        tap(app.navigationBars["New session"].buttons["Cancel"])
        XCTAssertTrue(session.waitForExistence(timeout: 5))

        tap(app.tabBars.buttons["Inbox"])
        let inboxSession = app.buttons["inbox-session-2"]
        XCTAssertTrue(inboxSession.waitForExistence(timeout: 10))
        inboxSession.tap()
        let menu = app.buttons["Session menu"]
        XCTAssertTrue(menu.waitForExistence(timeout: 10))
        tap(app.navigationBars.buttons.element(boundBy: 0))
        XCTAssertTrue(app.navigationBars["Sessions"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.tabBars.buttons["Sessions"].isSelected)
        tap(session)
        XCTAssertTrue(app.buttons["Stop current turn"].waitForExistence(timeout: 10))
        XCTAssertTrue(menu.waitForExistence(timeout: 10))
        menu.tap()
        tap(app.buttons["Session details"])
        XCTAssertFalse(app.buttons["Call this session"].exists)
        let inbox = app.buttons["Session inbox"]
        XCTAssertTrue(app.navigationBars["Session details"].waitForExistence(timeout: 5))
        let inspectorAnchor = app.staticTexts["session-inspector-anchor"]
        XCTAssertTrue(inspectorAnchor.waitForExistence(timeout: 5))
        let inspector = app.scrollViews.allElementsBoundByIndex.first { $0.identifier != "session-timeline" }
        XCTAssertNotNil(inspector)
        guard let inspector else { return }
        for _ in 0..<5 {
            if waitUntilHittable(inbox, timeout: 2) { break }
            inspector.swipeUp()
        }
        XCTAssertTrue(waitUntilHittable(inbox, timeout: 2))
        inbox.tap()
        let inboxEntry = app.buttons["inbox-session-1"]
        tap(inboxEntry)
        XCTAssertTrue(menu.waitForExistence(timeout: 5))
        XCTAssertFalse(app.navigationBars["Session details"].exists)
        let receipt = app.buttons["latest-instruction"]
        XCTAssertTrue(receipt.waitForExistence(timeout: 5))
        receipt.tap()
        tap(inboxEntry)
        XCTAssertTrue(menu.waitForExistence(timeout: 5))
        XCTAssertFalse(app.navigationBars["Session inbox"].exists)
    }

    private func tap(_ element: XCUIElement, file: StaticString = #filePath, line: UInt = #line) {
        XCTAssertTrue(element.waitForExistence(timeout: 5), "\(element) did not appear", file: file, line: line)
        element.tap()
    }

    private func waitUntilHittable(_ element: XCUIElement, timeout: TimeInterval) -> Bool {
        let predicate = NSPredicate(format: "hittable == true")
        return XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: predicate, object: element)], timeout: timeout) == .completed
    }
}
