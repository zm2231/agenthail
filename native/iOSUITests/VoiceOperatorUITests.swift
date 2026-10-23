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
}
