import XCTest

@MainActor
final class SessionNavigationTests: XCTestCase {
    func testANotificationAboutATurnOpensItsSessionAtThatTurnsReply() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-theme", "dark", "--preview-turns", "--preview-follow", "--preview-open-turn", "turn-5"]
        app.launch()
        XCTAssertTrue(app.textViews["composer-input"].waitForExistence(timeout: 15), "the notification opened its session")
        let timeline = app.scrollViews["session-timeline"]
        let reply = timeline.staticTexts.matching(NSPredicate(format: "label BEGINSWITH 'Reply for turn 5.'")).firstMatch
        XCTAssertTrue(reply.waitForExistence(timeout: 10), "the turn's reply is in the transcript")
        app.waitForSteadyJump()
        XCTAssertTrue(reply.isHittable, "the turn's reply is on screen")
        XCTAssertLessThan(reply.frame.minY, timeline.frame.midY, "the reply starts in the top half, so it reads from its first line")
        XCTAssertFalse(timeline.staticTexts["Reconnect is fixed"].isHittable, "the latest reply is not what opened")
        XCTAssertTrue(app.jumpToLatest.exists, "Jump to latest offers the rest of the session")
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "opened at turn 5"
        shot.lifetime = .keepAlways
        add(shot)
    }

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
        let list: XCUIElement = app.isPad ? app.buttons["session-computer::crowded-3"] : app
        for _ in 0..<3 where !more.isHittable { list.swipeUp() }
        XCTAssertEqual(more.label, "Show 3 more")
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "show more"
        shot.lifetime = .keepAlways
        add(shot)
        more.tapWhenReady()
        XCTAssertTrue(app.buttons["session-computer::crowded-4"].waitForExistence(timeout: 5), "Show more expands the project in place")
        for _ in 0..<3 where !more.isHittable { list.swipeUp() }
        XCTAssertEqual(more.label, "Show less")
        more.tapWhenReady()
        XCTAssertTrue(app.buttons["session-computer::crowded-3"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["session-computer::crowded-4"].exists, "Show less folds the project back to five")
        for _ in 0..<3 where !app.buttons["voice-entry"].isHittable { list.swipeDown() }
        app.buttons["voice-entry"].tapWhenReady()
        XCTAssertTrue(app.navigationBars["Talk to orchestrator"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["Sample conversation"].exists, "the preview voice entry must not dial a paired host")
        app.buttons["Done"].tapWhenReady()
        for tab in ["Sessions", "Inbox", "Settings"] {
            XCTAssertTrue(app.tab(tab).isHittable, "\(tab) tab is covered by the Voice entry")
        }
        app.tab("Inbox").tapWhenReady()
        XCTAssertTrue(app.tab("Inbox").isSelected)
        app.tab("Settings").tapWhenReady()
        XCTAssertTrue(app.tab("Settings").isSelected)
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
        XCTAssertEqual(app.tab("Inbox").isHittable, app.isPad, app.isPad ? "the tabs went away while a session is open beside the list" : "the tabs stay over a session")
        app.backToSessions()
        XCTAssertTrue(session.waitForExistence(timeout: 5))
        app.buttons["new-menu"].tapWhenReady()
        XCTAssertTrue(app.navigationBars["New session"].waitForExistence(timeout: 5))
        app.navigationBars["New session"].buttons["Cancel"].tapWhenReady()
        XCTAssertTrue(session.waitForExistence(timeout: 5))

        app.tab("Inbox").tapWhenReady()
        let inboxSession = app.buttons["inbox-session-2"]
        XCTAssertTrue(inboxSession.waitForExistence(timeout: 10))
        inboxSession.tapWhenReady()
        let menu = app.buttons["Session menu"]
        XCTAssertTrue(menu.waitForExistence(timeout: 10))
        app.backToSessions()
        XCTAssertTrue(app.navigationBars["Sessions"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.tab("Sessions").isSelected)
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
        app.tab("Inbox").tapWhenReady()
        XCTAssertTrue(app.buttons["inbox-session-1"].waitForExistence(timeout: 10), "Current lists what can still be acted on")
        app.buttons["History"].tapWhenReady()
        XCTAssertTrue(app.buttons["inbox-message-13"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["From @reviewer"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["The signing step passes now."].exists, "a peer message reads as its body")
        XCTAssertFalse(app.buttons["inbox-message-11"].exists, "a message waiting in the queue stays in Current")
        XCTAssertTrue(app.buttons["inbox-message-14"].waitForExistence(timeout: 12), "History reloads while it is on screen")
    }

    func testANotificationTappedAsAgenthailLaunchesOpensItsSession() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-route-at-launch"]
        app.launch()
        XCTAssertTrue(app.textViews["composer-input"].waitForExistence(timeout: 15), "a tap that arrived before the tabs appeared still opened its session")
    }

    func testANotificationTappedDuringACallClosesTheCallSheetAndTheCallGoesOn() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-voice-entry", "--preview-voice-live", "--preview-route-after", "8"]
        app.launch()
        let voice = app.buttons["voice-entry"]
        XCTAssertTrue(voice.waitForExistence(timeout: 10))
        voice.tapWhenReady()
        let call = app.navigationBars["Talk to orchestrator"]
        XCTAssertTrue(call.waitForExistence(timeout: 5))
        XCTAssertTrue(app.textViews["composer-input"].waitForExistence(timeout: 20), "the notification opened its session")
        XCTAssertTrue(call.waitForNonExistence(timeout: 5), "the call sheet closed instead of hiding the session")
        XCTAssertTrue(app.buttons["return-to-call"].waitForExistence(timeout: 5), "the live call is one tap away from the session")
        app.buttons["return-to-call"].tapWhenReady()
        XCTAssertTrue(call.waitForExistence(timeout: 5), "Return to call reopens the same call")
    }

    func testOpenFullTimelineInACallOpensTheSession() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-voice-entry"]
        app.launch()
        app.buttons["voice-entry"].tapWhenReady()
        let call = app.navigationBars["Talk to orchestrator"]
        XCTAssertTrue(call.waitForExistence(timeout: 5))
        let activity = app.descendants(matching: .any)["voice-activity"].firstMatch
        for _ in 0..<4 where !activity.isHittable { app.swipeUp() }
        activity.tapWhenReady()
        let timeline = app.buttons["Open full timeline"].firstMatch
        for _ in 0..<4 where !timeline.isHittable { app.swipeUp() }
        timeline.tapWhenReady()
        XCTAssertTrue(app.textViews["composer-input"].waitForExistence(timeout: 10), "Open full timeline opened the session")
        XCTAssertTrue(call.waitForNonExistence(timeout: 5), "the call sheet closed, so the session is not behind it")
        XCTAssertFalse(app.buttons["Back to Voice"].exists, "the session opens in the app, not in a second sheet")
    }

    func testANotificationDoesNotCloseARenameWithATypedNameAndClosingItShowsTheSession() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-route-on-draft"]
        app.launch()
        app.buttons["session-computer::demo"].tapWhenReady()
        app.buttons["session-summary"].tapWhenReady()
        let rename = app.buttons["Name conversation"]
        for _ in 0..<4 where !rename.isHittable { app.swipeUp() }
        rename.tapWhenReady()
        let field = app.descendants(matching: .any)["rename-session-name"]
        XCTAssertTrue(field.waitForExistence(timeout: 5))
        field.tap()
        field.typeText("keepme")
        let renaming = app.navigationBars["Name conversation"]
        XCTAssertTrue(app.descendants(matching: .any)["held-route"].waitForExistence(timeout: 10), "the rename sheet says a notification is waiting on it")
        XCTAssertTrue(renaming.exists, "the rename sheet with a typed name stayed open")
        XCTAssertTrue((field.value as? String ?? "").contains("keepme"), "the typed name survived: \(String(describing: field.value))")
        renaming.buttons["Cancel"].tapWhenReady()
        XCTAssertTrue(app.textViews["composer-input"].waitForExistence(timeout: 10), "closing the rename sheet shows the routed session")
        XCTAssertFalse(app.buttons["Name conversation"].isHittable, "the session details closed with it")
    }
}
