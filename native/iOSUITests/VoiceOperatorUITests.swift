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
