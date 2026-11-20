import XCTest

@MainActor
final class TranscriptScrollJumpUITests: XCTestCase {
    func testScrollingUpALongTranscriptMovesOnlyWithTheFinger() {
        scrollUp(["--preview-stream-paused"])
    }

    func testScrollingUpWhileTheAgentStreamsMovesOnlyWithTheFinger() {
        scrollUp([])
    }

    func testOpeningAtTheLatestReplyHoldsStillAndReachesAccessibility() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-reading", "--preview-theme", "dark"]
        app.launch()
        let latest = app.staticTexts["The latest update is visible above the composer."]
        XCTAssertTrue(latest.waitForExistence(timeout: 10), "the latest reply is not in the accessibility tree")
        let first = latest.frame.minY
        var spread: CGFloat = 0
        for _ in 0..<15 {
            RunLoop.current.run(until: Date().addingTimeInterval(0.1))
            spread = max(spread, abs(latest.frame.minY - first))
        }
        XCTAssertLessThan(spread, 2, "the latest reply moved \(spread) pt after the transcript opened")
    }

    private func scrollUp(_ extra: [String]) {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-stream", "--preview-theme", "dark", "--scroll-probe"] + extra
        app.launch()
        let timeline = app.scrollViews["session-timeline"]
        XCTAssertTrue(timeline.waitForExistence(timeout: 15))
        let probe = app.staticTexts["scroll-jumps"]
        XCTAssertTrue(probe.waitForExistence(timeout: 5))
        XCTAssertTrue(timeline.staticTexts.matching(NSPredicate(format: "label CONTAINS 'of reply '")).firstMatch.waitForExistence(timeout: 10), "the transcript showed no reply")
        let top = timeline.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.3))
        let bottom = timeline.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.75))
        for round in 0..<24 {
            if round % 3 == 2 {
                timeline.swipeDown()
            } else {
                top.press(forDuration: 0.05, thenDragTo: bottom, withVelocity: round % 3 == 0 ? 400 : 1200, thenHoldForDuration: 0.1)
            }
        }
        XCTAssertTrue(timeline.waitUntilRowsStill(), "the transcript kept moving after the last swipe")
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "after scrolling up"
        shot.lifetime = .keepAlways
        add(shot)
        let (jumps, worst, frame) = Self.parse(probe.label)
        let frames = XCTAttachment(string: "jumps=\(jumps) worst=\(worst) longest frame=\(frame) ms")
        frames.lifetime = .keepAlways
        add(frames)
        XCTAssertLessThanOrEqual(jumps, 1, "the transcript moved without the finger: \(probe.label)")
        XCTAssertLessThanOrEqual(worst, 45, "LazyVStack's one re-estimate of unmeasured rows missed by more than 45 pt: \(probe.label)")
    }

    static func parse(_ label: String) -> (jumps: Int, worst: Int, frame: Int) {
        guard let match = label.wholeMatch(of: /jumps=(\d+) worst=(\d+) frame=(\d+)/), let jumps = Int(match.1), let worst = Int(match.2), let frame = Int(match.3) else { return (.max, .max, .max) }
        return (jumps, worst, frame)
    }

    func testTheProbeLabelParsesOnlyItsExactForm() {
        XCTAssertEqual(Self.parse("jumps=1 worst=57 frame=67").jumps, 1)
        XCTAssertEqual(Self.parse("jumps=1 worst=57 frame=67").worst, 57)
        XCTAssertEqual(Self.parse("jumps=1 worst=57 frame=67").frame, 67)
        for bad in ["jumps=-1 worst=-1 frame=1", "jumps=0", "jumps=0 worst=3", "jumps=0 worst=3 frame=1 extra=1", "worst=3 jumps=0 frame=1", "", "jumps=0 worst=3 frame=3.5"] {
            XCTAssertEqual(Self.parse(bad).jumps, .max, bad)
        }
    }
}
