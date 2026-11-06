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
        let (jumps, worst) = Self.parse(probe.label)
        XCTAssertLessThanOrEqual(jumps, 1, "the transcript moved without the finger: \(probe.label)")
        XCTAssertLessThanOrEqual(worst, 80, "LazyVStack's one re-estimate of unmeasured rows missed by more than 80 pt: \(probe.label)")
    }

    private static func parse(_ label: String) -> (jumps: Int, worst: Int) {
        let values = Dictionary(uniqueKeysWithValues: label.split(separator: " ").compactMap { pair -> (String, Int)? in
            let parts = pair.split(separator: "=")
            guard parts.count == 2, let value = Int(parts[1]) else { return nil }
            return (String(parts[0]), value)
        })
        return (values["jumps"] ?? .max, values["worst"] ?? .max)
    }
}
