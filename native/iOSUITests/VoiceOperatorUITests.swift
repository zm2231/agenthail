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
        newConversation.tap()
        XCTAssertTrue(app.staticTexts["This creates a separate Codex thread. The current conversation remains available in Sessions."].waitForExistence(timeout: 5))
    }

    func testFollowingLatestDoesNotMoveTheTranscriptViewport() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-voice"]
        app.launch()
        let newest = app.staticTexts["Newest voice update"]
        XCTAssertTrue(newest.waitForExistence(timeout: 10))
        XCTAssertTrue(newest.isHittable)
        let position = newest.frame.minY
        let scroll = app.scrollViews.firstMatch
        scroll.swipeDown()
        let jump = app.buttons["Jump to latest voice activity"]
        XCTAssertTrue(jump.waitForExistence(timeout: 5))
        jump.tap()
        let returned = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            newest.isHittable && abs(newest.frame.minY - position) < 3
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [returned], timeout: 5), .completed)
        XCTAssertEqual(newest.frame.minY, position, accuracy: 3)
        XCTAssertFalse(jump.isHittable)
        let call = app.buttons["Start voice call"]
        for _ in 0..<4 {
            Thread.sleep(forTimeInterval: 0.5)
            XCTAssertEqual(newest.frame.minY, position, accuracy: 3)
            XCTAssertTrue(call.isEnabled)
            XCTAssertFalse(jump.isHittable)
        }
    }
}
