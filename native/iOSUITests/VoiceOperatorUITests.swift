import XCTest

@MainActor
final class VoiceOperatorUITests: XCTestCase {
    func testPreviewStartsAtNewestTranscriptAndExplainsNewConversation() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-voice"]
        app.launch()

        let newest = app.staticTexts["Newest voice update"]
        XCTAssertTrue(newest.waitForExistence(timeout: 10))
        XCTAssertTrue(newest.isHittable)

        let newConversation = app.buttons["new-voice-conversation"]
        XCTAssertTrue(newConversation.isHittable)
        newConversation.tapWhenReady()
        XCTAssertTrue(app.staticTexts["This creates a separate Codex thread. The current conversation remains available in Sessions."].waitForExistence(timeout: 5))
    }

    func testJumpToLatestReachesTheBottomWithoutMovingTheControls() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-voice"]
        app.launch()

        let newest = app.staticTexts["Newest voice update"]
        XCTAssertTrue(newest.waitForExistence(timeout: 10))
        let call = app.buttons["Start voice call"]
        XCTAssertTrue(call.waitForExistence(timeout: 5))
        let controls = call.frame.minY
        let scroll = app.scrollViews.firstMatch
        let jump = app.buttons["Jump to latest voice activity"]

        scroll.swipeUp()
        scroll.swipeUp()
        let settled = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in !jump.exists }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [settled], timeout: 5), .completed)
        Thread.sleep(forTimeInterval: 1)
        let bottom = newest.frame.minY

        scroll.swipeDown()
        XCTAssertTrue(jump.waitForExistence(timeout: 5))
        XCTAssertEqual(call.frame.minY, controls, accuracy: 1)
        jump.tapWhenReady()

        let returned = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            !jump.exists && abs(newest.frame.minY - bottom) < 3
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [returned], timeout: 5), .completed)
        XCTAssertEqual(newest.frame.minY, bottom, accuracy: 3)
        XCTAssertEqual(call.frame.minY, controls, accuracy: 1)
        for _ in 0..<4 {
            Thread.sleep(forTimeInterval: 0.5)
            XCTAssertEqual(newest.frame.minY, bottom, accuracy: 3)
            XCTAssertFalse(jump.exists)
        }
    }

    func testAgentActivityReadsInOrderAndKeepsEachRowsIdentity() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-voice"]
        app.launch()

        let activity = app.buttons["voice-activity"]
        XCTAssertTrue(activity.waitForExistence(timeout: 10))
        activity.tapWhenReady()
        let request = app.staticTexts["The build fails after reconnecting. Trace it, fix it, and keep a regression test."]
        let tools = app.buttons["tool-3"]
        XCTAssertTrue(tools.waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["activity-3"].exists, "a short run shows its lines without a summary")
        XCTAssertTrue(request.exists)
        XCTAssertLessThan(request.frame.minY, tools.frame.minY)
        // Expanded activity can open below the call controls; bring the row
        // above them before tapping it.
        let controls = app.buttons["Start voice call"]
        for _ in 0..<4 where tools.frame.maxY > controls.frame.minY { app.scrollViews.firstMatch.swipeUp() }
        XCTAssertLessThan(tools.frame.maxY, controls.frame.minY)
        tools.tapWhenReady()
        XCTAssertTrue(app.buttons["collapse-tool-3"].waitForExistence(timeout: 5))
    }

    func testTheCallScreenOpensAtTheBottomItCanScrollBackTo() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-voice"]
        app.launch()

        let newest = app.staticTexts["Newest voice update"]
        XCTAssertTrue(newest.waitForExistence(timeout: 10))
        let opened = settledMinY(of: newest)
        let scroll = app.scrollViews.firstMatch
        scroll.swipeDown()
        scroll.swipeUp()
        scroll.swipeUp()
        XCTAssertEqual(settledMinY(of: newest), opened, accuracy: 3, "the screen opened past the bottom it can scroll back to")
    }

    // The element's position once it has stopped moving for half a second.
    private func settledMinY(of element: XCUIElement, timeout: TimeInterval = 10) -> CGFloat {
        var last = element.frame.minY
        var stableSince = Date()
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            RunLoop.current.run(until: Date().addingTimeInterval(0.1))
            let now = element.frame.minY
            if abs(now - last) > 0.5 { last = now; stableSince = Date() }
            else if Date().timeIntervalSince(stableSince) >= 0.5 { return now }
        }
        XCTFail("\(element) kept moving for \(timeout) seconds")
        return last
    }
}
