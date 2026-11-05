import XCTest

@MainActor
final class TranscriptFollowGrowthUITests: XCTestCase {
    private func launch(_ extra: [String]) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-theme", "dark", "--preview-reading", "--preview-follow"] + extra
        app.launch()
        XCTAssertTrue(app.textViews["composer-input"].waitForExistence(timeout: 15))
        return app
    }

    private func lastRow(_ app: XCUIApplication) -> XCUIElement { app.descendants(matching: .any)["queue-computer/25"] }

    private func covered(_ app: XCUIApplication) -> CGFloat {
        lastRow(app).frame.maxY - app.textViews["composer-input"].frame.minY
    }

    func testAStreamGrowingTheTranscriptDoesNotPullBackAReaderWhoScrolledUp() {
        let app = launch(["--preview-stream"])
        let timeline = app.scrollViews["session-timeline"]
        sleep(2)
        timeline.swipeDown(velocity: .slow)
        XCTAssertTrue(app.buttons["Jump to latest"].waitForExistence(timeout: 3))
        let anchor = timeline.staticTexts.allElementsBoundByIndex.first { timeline.frame.contains(CGPoint(x: $0.frame.midX, y: $0.frame.midY)) && !$0.label.isEmpty }
        guard let anchor else { return XCTFail("no text on screen after scrolling up") }
        let (label, before) = (anchor.label, anchor.frame.minY)
        sleep(5)
        let after = timeline.staticTexts.matching(NSPredicate(format: "label == %@", label)).firstMatch.frame.minY
        XCTAssertEqual(after, before, accuracy: 2, "the stream moved a reader who scrolled up")
        XCTAssertTrue(app.buttons["Jump to latest"].exists, "the reader was pulled back to the end")
    }

    func testJumpShowsWheneverTheLastRowIsCovered() {
        let app = launch(["--preview-tail", "-preview-tail", "many"])
        XCTAssertTrue(lastRow(app).waitForExistence(timeout: 5))
        let timeline = app.scrollViews["session-timeline"]
        var checked = 0
        for step in 1...6 {
            let start = timeline.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5))
            start.press(forDuration: 0.05, thenDragTo: start.withOffset(CGVector(dx: 0, dy: 25)), withVelocity: 60, thenHoldForDuration: 0.6)
            RunLoop.current.run(until: Date().addingTimeInterval(0.8))
            guard lastRow(app).exists, covered(app) > 1 else { continue }
            checked += 1
            XCTAssertTrue(app.buttons["Jump to latest"].exists, "the last row is \(Int(covered(app))) pt under the composer after step \(step) and nothing offers the way back")
        }
        XCTAssertGreaterThan(checked, 0, "no step left the last row under the composer")
    }

    func testAFollowThatNeverLandsStillOffersJump() {
        let app = launch(["--preview-stream", "--preview-follow-stuck"])
        XCTAssertTrue(app.buttons["Jump to latest"].waitForExistence(timeout: 6), "following stayed short of the end and nothing offered the way back")
        app.buttons["Jump to latest"].tap()
        XCTAssertTrue(app.buttons["Jump to latest"].waitForNonExistence(timeout: 3), "Jump to latest did not reach the end")
    }

    func testPullingPastTheEndShowsNoJumpAndKeepsFollowing() {
        let app = launch(["--preview-tail", "-preview-tail", "many"])
        XCTAssertTrue(lastRow(app).waitForExistence(timeout: 5))
        sleep(4)
        let timeline = app.scrollViews["session-timeline"]
        let start = timeline.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.6))
        start.press(forDuration: 0.05, thenDragTo: start.withOffset(CGVector(dx: 0, dy: -80)), withVelocity: 200, thenHoldForDuration: 0.3)
        XCTAssertFalse(app.buttons["Jump to latest"].exists, "pulling past the end offered Jump to latest")
        sleep(1)
        XCTAssertFalse(app.buttons["Jump to latest"].exists)
        XCTAssertLessThanOrEqual(covered(app), 0, "the last row ended up under the composer after pulling past the end")
    }

    func testStreamingWithTheKeyboardUpScrollsAtAModestRate() {
        let app = launch(["--preview-stream", "--scroll-count"])
        let composer = app.textViews["composer-input"]
        composer.tapWhenReady()
        composer.typeText(" ")
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5))
        let count = app.staticTexts["scroll-count"]
        XCTAssertTrue(count.waitForExistence(timeout: 5))
        let before = Int(count.label) ?? 0
        var jumpSeen = 0
        let start = Date()
        while Date().timeIntervalSince(start) < 6 {
            if app.buttons["Jump to latest"].exists { jumpSeen += 1 }
            RunLoop.current.run(until: Date().addingTimeInterval(0.25))
        }
        let after = Int(count.label) ?? 0
        XCTAssertEqual(jumpSeen, 0, "Jump to latest flickered while following a stream with the keyboard up")
        let rate = Double(after - before) / 6
        print("SCROLL-RATE \(rate)")
        XCTAssertLessThanOrEqual(rate, 12, "the transcript scrolled \(rate) times a second while streaming with the keyboard up")
        XCTAssertEqual(app.state, .runningForeground)
        XCTAssertTrue(app.keyboards.firstMatch.exists, "the keyboard went away while streaming")
    }
}
