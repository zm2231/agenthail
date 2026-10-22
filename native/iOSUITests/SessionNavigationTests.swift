import XCTest

@MainActor
final class SessionNavigationTests: XCTestCase {
    func testVoiceEntryPreservesTabNavigation() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-voice-entry"]
        app.launch()
        XCTAssertTrue(app.buttons["voice-entry"].waitForExistence(timeout: 10))
        let workspace = app.buttons["project-computer::project-fieldnotes"]
        XCTAssertTrue(workspace.waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["session-computer::codex-demo"].waitForExistence(timeout: 5))
        workspace.tapWhenReady()
        XCTAssertFalse(app.buttons["session-computer::codex-demo"].exists)
        XCTAssertTrue(app.buttons["session-computer::demo"].exists, "a collapsed project keeps its working session")
        workspace.tapWhenReady()
        XCTAssertTrue(app.buttons["session-computer::codex-demo"].waitForExistence(timeout: 5))
        app.buttons["voice-entry"].tapWhenReady()
        XCTAssertTrue(app.navigationBars["Talk to orchestrator"].waitForExistence(timeout: 5))
        app.buttons["Done"].tapWhenReady()
        for tab in ["Sessions", "Inbox", "Settings"] {
            XCTAssertTrue(app.tabBars.buttons[tab].isHittable, "\(tab) tab is covered by the Voice entry")
        }
        app.tabBars.buttons["Inbox"].tapWhenReady()
        XCTAssertTrue(app.tabBars.buttons["Inbox"].isSelected)
        app.tabBars.buttons["Settings"].tapWhenReady()
        XCTAssertTrue(app.tabBars.buttons["Settings"].isSelected)
        XCTAssertFalse(app.buttons["voice-entry"].exists)
    }

    func testSessionAndInboxNavigationReturnToTheirOrigin() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-delivery"]
        app.launch()
        let session = app.buttons["session-computer::demo"]
        XCTAssertTrue(session.waitForExistence(timeout: 10))
        session.tapWhenReady()
        XCTAssertFalse(app.buttons["voice-entry"].exists)
        XCTAssertFalse(app.staticTexts["Working on your Mac"].exists)
        XCTAssertTrue(app.buttons["Session menu"].waitForExistence(timeout: 10))
        app.buttons["Session menu"].tapWhenReady()
        let allEvents = app.buttons["All events"]
        let call = app.buttons["Call this session"]
        XCTAssertTrue(allEvents.waitForExistence(timeout: 5))
        XCTAssertTrue(call.waitForExistence(timeout: 5))
        XCTAssertLessThan(allEvents.frame.maxY, call.frame.minY)
        app.buttons["Chat"].tapWhenReady()
        XCTAssertTrue(app.buttons["Stop current turn"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.tabBars.buttons["Inbox"].isHittable)
        app.navigationBars.buttons.element(boundBy: 0).tapWhenReady()
        XCTAssertTrue(session.waitForExistence(timeout: 5))
        app.buttons["new-session"].tapWhenReady()
        XCTAssertTrue(app.navigationBars["New session"].waitForExistence(timeout: 5))
        app.navigationBars["New session"].buttons["Cancel"].tapWhenReady()
        XCTAssertTrue(session.waitForExistence(timeout: 5))

        app.tabBars.buttons["Inbox"].tapWhenReady()
        let inboxSession = app.buttons["inbox-session-2"]
        XCTAssertTrue(inboxSession.waitForExistence(timeout: 10))
        inboxSession.tapWhenReady()
        let menu = app.buttons["Session menu"]
        XCTAssertTrue(menu.waitForExistence(timeout: 10))
        app.navigationBars.buttons.element(boundBy: 0).tapWhenReady()
        XCTAssertTrue(app.navigationBars["Sessions"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.tabBars.buttons["Sessions"].isSelected)
        session.tapWhenReady()
        XCTAssertTrue(app.buttons["Stop current turn"].waitForExistence(timeout: 10))
        XCTAssertTrue(menu.waitForExistence(timeout: 10))
        menu.tapWhenReady()
        app.buttons["Session details"].tapWhenReady()
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
        inbox.tapWhenReady()
        let inboxEntry = app.buttons["inbox-session-1"]
        inboxEntry.tapWhenReady()
        XCTAssertTrue(menu.waitForExistence(timeout: 5))
        XCTAssertFalse(app.navigationBars["Session details"].exists)
        let receipt = app.buttons["latest-instruction"]
        XCTAssertTrue(receipt.waitForExistence(timeout: 5))
        receipt.tapWhenReady()
        inboxEntry.tapWhenReady()
        XCTAssertTrue(menu.waitForExistence(timeout: 5))
        XCTAssertFalse(app.navigationBars["Session inbox"].exists)
    }

    private func waitUntilHittable(_ element: XCUIElement, timeout: TimeInterval) -> Bool {
        let predicate = NSPredicate(format: "hittable == true")
        return XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: predicate, object: element)], timeout: timeout) == .completed
    }
}
