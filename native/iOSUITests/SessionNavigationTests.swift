import XCTest

@MainActor
final class SessionNavigationTests: XCTestCase {
    func testVoiceEntryPreservesTabNavigation() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-voice-entry"]
        app.launch()
        XCTAssertTrue(app.buttons["voice-entry"].waitForExistence(timeout: 10))
        let workspace = app.buttons["workspace-/Users/demo/projects/fieldnotes"]
        XCTAssertTrue(workspace.waitForExistence(timeout: 5))
        workspace.tap()
        XCTAssertFalse(app.buttons["session-demo"].exists)
        workspace.tap()
        XCTAssertTrue(app.buttons["session-demo"].waitForExistence(timeout: 5))
        app.buttons["voice-entry"].tap()
        XCTAssertTrue(app.navigationBars["Talk to orchestrator"].waitForExistence(timeout: 5))
        app.buttons["Done"].tap()
        for tab in ["Sessions", "Inbox", "Settings"] {
            XCTAssertTrue(app.tabBars.buttons[tab].isHittable, "\(tab) tab is covered by the Voice entry")
        }
        app.tabBars.buttons["Inbox"].tap()
        XCTAssertTrue(app.tabBars.buttons["Inbox"].isSelected)
        app.tabBars.buttons["Settings"].tap()
        XCTAssertTrue(app.tabBars.buttons["Settings"].isSelected)
        XCTAssertFalse(app.buttons["voice-entry"].exists)
    }


    func testSessionOpensAndReturnsToBrowser() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app"]
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
        app.buttons["Chat"].tap()
        XCTAssertTrue(app.buttons["Stop current turn"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.tabBars.buttons["Inbox"].isHittable)
        app.navigationBars.buttons.element(boundBy: 0).tap()
        XCTAssertTrue(session.waitForExistence(timeout: 5))
        app.buttons["new-session"].tap()
        XCTAssertTrue(app.navigationBars["New session"].waitForExistence(timeout: 5))
    }

    func testInboxNavigationReturnsToSessionsAndFromInspectorAndComposer() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-delivery"]
        app.launch()
        app.tabBars.buttons["Inbox"].tap()
        let inboxSession = app.buttons["inbox-session-2"]
        XCTAssertTrue(inboxSession.waitForExistence(timeout: 10))
        inboxSession.tap()
        let menu = app.buttons["Session menu"]
        XCTAssertTrue(menu.waitForExistence(timeout: 10))
        app.navigationBars.buttons.element(boundBy: 0).tap()
        XCTAssertTrue(app.navigationBars["Sessions"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.tabBars.buttons["Sessions"].isSelected)
        app.buttons["session-demo"].tap()
        XCTAssertTrue(app.buttons["Stop current turn"].waitForExistence(timeout: 10))
        XCTAssertTrue(menu.waitForExistence(timeout: 10))
        menu.tap()
        app.buttons["Session details"].tap()
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
        app.buttons["inbox-session-1"].tap()
        XCTAssertTrue(menu.waitForExistence(timeout: 5))
        XCTAssertFalse(app.navigationBars["Session details"].exists)
        let receipt = app.buttons["latest-instruction"]
        XCTAssertTrue(receipt.waitForExistence(timeout: 5))
        receipt.tap()
        app.buttons["inbox-session-1"].tap()
        XCTAssertTrue(menu.waitForExistence(timeout: 5))
        XCTAssertFalse(app.navigationBars["Session inbox"].exists)
    }



    private func waitUntilHittable(_ element: XCUIElement, timeout: TimeInterval) -> Bool {
        let predicate = NSPredicate(format: "hittable == true")
        return XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: predicate, object: element)], timeout: timeout) == .completed
    }
}
