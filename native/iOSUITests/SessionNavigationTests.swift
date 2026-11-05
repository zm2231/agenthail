import XCTest

@MainActor
final class SessionNavigationTests: XCTestCase {
    func testVoiceEntryPreservesTabNavigation() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-voice-entry", "--preview-crowded"]
        app.launch()
        XCTAssertTrue(app.buttons["voice-entry"].waitForExistence(timeout: 10))
        let workspace = app.descendants(matching: .any)["project-computer::project-fieldnotes"]
        XCTAssertTrue(workspace.waitForExistence(timeout: 5))
        XCTAssertFalse((workspace.value as? String ?? "").contains("collapsed"), "the header does not toggle, so it does not say collapsed: \(String(describing: workspace.value))")
        XCTAssertTrue(app.buttons["session-computer::demo"].exists, "a project shows its working session")
        XCTAssertTrue(app.buttons["session-computer::crowded-3"].exists, "a project shows the daemon's five")
        XCTAssertFalse(app.buttons["session-computer::crowded-4"].exists, "a sixth idle session waits behind Show more")
        let more = app.buttons["more-computer::project-fieldnotes"]
        XCTAssertEqual(more.label, "Show 3 more")
        app.swipeUp()
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "show more"
        shot.lifetime = .keepAlways
        add(shot)
        more.tapWhenReady()
        XCTAssertTrue(app.buttons["session-computer::crowded-4"].waitForExistence(timeout: 5), "Show more expands the project in place")
        for _ in 0..<3 where !more.isHittable { app.swipeUp() }
        XCTAssertEqual(more.label, "Show less")
        more.tapWhenReady()
        XCTAssertTrue(app.buttons["session-computer::crowded-3"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["session-computer::crowded-4"].exists, "Show less folds the project back to five")
        for _ in 0..<3 where !app.buttons["voice-entry"].isHittable { app.swipeDown() }
        app.buttons["voice-entry"].tapWhenReady()
        XCTAssertTrue(app.navigationBars["Talk to orchestrator"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["Sample conversation"].exists, "the preview voice entry must not dial a paired host")
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
        app.buttons["new-menu"].tapWhenReady()
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
        XCTAssertTrue(app.navigationBars["Session details"].waitForExistence(timeout: 5))
        let inspectorAnchor = app.staticTexts["session-inspector-anchor"]
        XCTAssertTrue(inspectorAnchor.waitForExistence(timeout: 5))
        let inspector = app.scrollViews.allElementsBoundByIndex.first { $0.identifier != "session-timeline" }
        XCTAssertNotNil(inspector)
        guard let inspector else { return }
        for _ in 0..<5 {
            XCTAssertFalse(app.buttons["Session inbox"].exists)
            inspector.swipeUp()
        }
    }

    func testInboxHistoryShowsANewMessageWithinSeconds() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-delivery"]
        app.launch()
        app.tabBars.buttons["Inbox"].tapWhenReady()
        XCTAssertTrue(app.buttons["inbox-session-1"].waitForExistence(timeout: 10), "Current lists what can still be acted on")
        app.buttons["History"].tapWhenReady()
        XCTAssertTrue(app.buttons["inbox-message-13"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["From @reviewer"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["The signing step passes now."].exists, "a peer message reads as its body")
        XCTAssertFalse(app.buttons["inbox-message-11"].exists, "a message waiting in the queue stays in Current")
        XCTAssertTrue(app.buttons["inbox-message-14"].waitForExistence(timeout: 12), "History reloads while it is on screen")
    }
}
