import XCTest

@MainActor
final class VoiceOperatorUITests: XCTestCase {
    func testPreviewStartsAtNewestTranscriptAndExplainsNewConversation() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-voice", "--preview-voice-refresh"]
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
        app.launchArguments = ["--preview-voice", "--preview-voice-refresh"]
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

    func testVoiceActivityUsesSessionTranscriptRowsAndStopsFollowing() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-voice", "--preview-voice-refresh"]
        app.launch()
        let activity = app.buttons["voice-activity"]
        XCTAssertTrue(activity.waitForExistence(timeout: 10))
        activity.tap()
        let commandGroup = app.buttons["activity-3"]
        for _ in 0..<4 where !commandGroup.isHittable { app.scrollViews.firstMatch.swipeUp() }
        XCTAssertTrue(commandGroup.isHittable)
        commandGroup.tap()
        let command = app.staticTexts["Run command"]
        XCTAssertTrue(command.waitForExistence(timeout: 5))
        let call = app.buttons["Start voice call"]
        XCTAssertTrue(call.isEnabled)
        let position = command.frame.minY
        for _ in 0..<4 {
            Thread.sleep(forTimeInterval: 0.3)
            XCTAssertEqual(command.frame.minY, position, accuracy: 3)
            XCTAssertTrue(call.isEnabled)
        }
    }
}
