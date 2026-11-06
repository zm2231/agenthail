import XCTest

@MainActor
final class TranscriptScrollJumpUITests: XCTestCase {
    func testScrollingUpALongTranscriptMovesOnlyWithTheFinger() {
        scrollUp(["--preview-stream-paused"])
    }

    func testScrollingUpWhileTheAgentStreamsMovesOnlyWithTheFinger() {
        scrollUp([])
    }

    private func scrollUp(_ extra: [String]) {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-stream", "--preview-theme", "dark", "--scroll-probe"] + extra
        app.launch()
        let timeline = app.scrollViews["session-timeline"]
        XCTAssertTrue(timeline.waitForExistence(timeout: 15))
        sleep(2)
        let probe = app.staticTexts["scroll-jumps"]
        XCTAssertTrue(probe.waitForExistence(timeout: 5))
        let top = timeline.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.3))
        let bottom = timeline.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.75))
        for round in 0..<24 {
            if round % 3 == 2 {
                timeline.swipeDown()
            } else {
                top.press(forDuration: 0.05, thenDragTo: bottom, withVelocity: round % 3 == 0 ? 400 : 1200, thenHoldForDuration: 0.1)
            }
        }
        sleep(2)
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "after scrolling up"
        shot.lifetime = .keepAlways
        add(shot)
        XCTAssertTrue(probe.label.hasPrefix("jumps=0 "), "the transcript moved without the finger: \(probe.label)")
    }
}
