import XCTest

@MainActor
final class TranscriptQueueTailUITests: XCTestCase {
    private func launch(_ rows: String = "one", extra: [String] = []) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-tail", "-preview-tail", rows, "--preview-theme", "dark", "--preview-reading", "--preview-follow"] + extra
        app.launch()
        XCTAssertTrue(app.textViews["composer-input"].waitForExistence(timeout: 15))
        if rows != "none" {
            let first = app.descendants(matching: .any).matching(NSPredicate(format: "identifier BEGINSWITH 'queue-computer/'")).firstMatch
            XCTAssertTrue(first.waitForExistence(timeout: 10), "no queued row arrived")
            XCTAssertTrue(first.waitUntilStill(), "the queued rows kept moving")
        }
        return app
    }

    private func settle() { RunLoop.current.run(until: Date().addingTimeInterval(1)) }

    // The preview's --preview-tail-slow and --preview-reopen-slow delays, counted from the launch or the reopen.
    private static let lateRowDelay: TimeInterval = 5
    private static let reopenDelay: TimeInterval = 6

    private func waitPast(_ start: Date, delay: TimeInterval) {
        let end = start.addingTimeInterval(delay + 2.5)
        if end > Date() { Thread.sleep(until: end) }
    }

    private func record(_ app: XCUIApplication, _ name: String) {
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }

    private func row(_ id: Int, _ app: XCUIApplication) -> XCUIElement {
        app.descendants(matching: .any)["queue-computer/\(id)"]
    }

    private func assertNotCovered(_ element: XCUIElement, _ app: XCUIApplication, _ context: String, line: UInt = #line) {
        XCTAssertTrue(element.exists, "no queued row \(context)", line: line)
        let top = app.textViews["composer-input"].frame.minY
        XCTAssertLessThanOrEqual(element.frame.maxY, top, "the composer covers the queued row \(context)", line: line)
        XCTAssertGreaterThan(element.frame.minY, app.scrollViews["session-timeline"].frame.minY, "the row is off the top \(context)", line: line)
    }

    private func assertAtLatest(_ app: XCUIApplication, _ context: String, line: UInt = #line) {
        let jump = app.buttons["Jump to latest"]
        for _ in 0..<8 {
            RunLoop.current.run(until: Date().addingTimeInterval(0.15))
            XCTAssertFalse(jump.exists, "the transcript left its latest row \(context)", line: line)
        }
    }

    private func midScreenText(_ app: XCUIApplication) throws -> String {
        let timeline = app.scrollViews["session-timeline"]
        let band = timeline.frame.insetBy(dx: 0, dy: timeline.frame.height * 0.2)
        var pending = [try timeline.snapshot()]
        while let next = pending.popLast() {
            if next.elementType == .staticText, !next.label.isEmpty, band.contains(CGPoint(x: next.frame.midX, y: next.frame.midY)) { return next.label }
            pending += next.children
        }
        throw NSError(domain: "TranscriptQueueTailUITests", code: 1, userInfo: [NSLocalizedDescriptionKey: "no text mid-screen"])
    }

    func testAQueuedRowIsTheLastRowNotCoveredAndNoJump() {
        let app = launch()
        assertNotCovered(row(21, app), app, "after opening")
        assertAtLatest(app, "after opening")
    }

    func testARowArrivingWhileFollowingIsNotCoveredAndNoJump() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-tail", "-preview-tail", "one", "--preview-theme", "dark", "--preview-reading", "--preview-follow", "--preview-tail-late", "--preview-tail-slow"]
        app.launch()
        XCTAssertTrue(app.textViews["composer-input"].waitForExistence(timeout: 15))
        XCTAssertFalse(row(21, app).exists, "the row hasn't arrived yet")
        assertAtLatest(app, "before the row arrived")
        XCTAssertTrue(row(21, app).waitForExistence(timeout: 10), "the row arrived")
        XCTAssertTrue(row(21, app).waitUntilStill(), "the late row kept moving")
        assertNotCovered(row(21, app), app, "after it arrived late")
        assertAtLatest(app, "after a row arrived late")
    }

    func testARowArrivingDoesNotMoveATranscriptScrolledUp() throws {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-tail", "-preview-tail", "one", "--preview-theme", "dark", "--preview-reading", "--preview-follow", "--preview-tail-late", "--preview-tail-slow"]
        app.launch()
        let launched = Date()
        XCTAssertTrue(app.textViews["composer-input"].waitForExistence(timeout: 15))
        XCTAssertFalse(row(21, app).exists, "the row hasn't arrived yet")
        let timeline = app.scrollViews["session-timeline"]
        timeline.swipeDown(velocity: .slow)
        XCTAssertTrue(app.buttons["Jump to latest"].waitForExistence(timeout: 2))
        let label = try midScreenText(app)
        let before = timeline.staticTexts[label].frame.minY
        waitPast(launched, delay: Self.lateRowDelay)
        XCTAssertEqual(timeline.staticTexts[label].frame.minY, before, accuracy: 2, "the row's arrival moved a transcript scrolled up")
        app.buttons["Jump to latest"].tapWhenReady()
        XCTAssertTrue(row(21, app).waitForExistence(timeout: 5), "the row did arrive")
    }

    func testScrollingUpWhileAReopenedSessionLoadsIsNotUndone() throws {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-theme", "dark", "--preview-reading", "--preview-follow", "--preview-reopen-slow"]
        app.launch()
        let session = app.buttons["session-computer::demo"]
        session.tapWhenReady(timeout: 15)
        XCTAssertTrue(app.textViews["composer-input"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["The latest update is visible above the composer."].waitForExistence(timeout: 10), "the first open did not load")
        XCTAssertTrue(app.scrollViews["session-timeline"].waitUntilRowsStill(), "the first open kept moving")
        app.backToSessions()
        session.tapWhenReady()
        let reopened = Date()
        let timeline = app.scrollViews["session-timeline"]
        XCTAssertTrue(timeline.waitForExistence(timeout: 5), "the cached transcript shows while it reloads")
        timeline.swipeDown(velocity: .slow)
        XCTAssertTrue(app.buttons["Jump to latest"].waitForExistence(timeout: 2))
        let label = try midScreenText(app)
        let before = timeline.staticTexts[label].frame.minY
        waitPast(reopened, delay: Self.reopenDelay)
        XCTAssertEqual(timeline.staticTexts[label].frame.minY, before, accuracy: 2, "the landing undid the reader's scroll")
        XCTAssertTrue(app.buttons["Jump to latest"].exists)
    }

    private func open(_ session: XCUIElement, in app: XCUIApplication) {
        if !session.isHittable { app.backToSessions() }
        session.tapWhenReady(timeout: 15)
        XCTAssertTrue(app.textViews["composer-input"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.scrollViews["session-timeline"].waitUntilRowsStill(), "the opened session kept moving")
    }

    private func gapAboveComposer(_ app: XCUIApplication) throws -> CGFloat {
        let top = app.textViews["composer-input"].frame.minY
        var pending = [try app.scrollViews["session-timeline"].snapshot()]
        var lowest: CGFloat = 0
        while let next = pending.popLast() {
            if next.elementType == .staticText, next.frame.minY < top { lowest = max(lowest, next.frame.maxY) }
            pending += next.children
        }
        return top - lowest
    }

    func testSwitchingFromATallQueueToAnEmptySessionLeavesNoRows() throws {
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCUIDevice.shared.orientation = .landscapeLeft
            addTeardownBlock { @MainActor in XCUIDevice.shared.orientation = .portrait }
        }
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-tail", "-preview-tail", "many", "--preview-theme", "dark", "--preview-reading", "--preview-follow"]
        app.launch()
        let queued = app.buttons["session-computer::demo"]
        let empty = app.buttons["session-computer::codex-demo"]

        open(empty, in: app)
        XCTAssertFalse(row(25, app).exists, "the empty session shows a queued row")
        let clear = try gapAboveComposer(app)
        XCTAssertLessThan(clear, 120, "the empty session is too short to show leftover space")

        open(queued, in: app)
        XCTAssertTrue(row(25, app).waitForExistence(timeout: 10))
        app.buttons["queue-tail-more"].tapWhenReady()
        XCTAssertTrue(row(21, app).waitForExistence(timeout: 5), "the tall queue is open")

        open(empty, in: app)
        XCTAssertFalse(row(25, app).exists, "the rows followed to the empty session")
        XCTAssertFalse(app.buttons["queue-tail-more"].exists)
        assertAtLatest(app, "after switching to the empty session")
        XCTAssertEqual(try gapAboveComposer(app), clear, accuracy: 24, "the newest row isn't in place after the switch")

        open(queued, in: app)
        XCTAssertTrue(row(25, app).waitForExistence(timeout: 10), "the rows are back on their own session")
        XCTAssertTrue(app.buttons["queue-tail-more"].exists, "and collapsed again")
    }

    func testTheCapShowsThreeAndOpensTheRest() {
        let app = launch("many")
        let more = app.buttons["queue-tail-more"]
        XCTAssertTrue(more.exists)
        XCTAssertEqual(more.label, "2 more queued")
        XCTAssertGreaterThanOrEqual(more.frame.height, minimumTapTarget)
        XCTAssertFalse(row(21, app).exists)
        assertNotCovered(row(25, app), app, "with the rest collapsed")
        assertAtLatest(app, "with the rest collapsed")
        more.tap()
        XCTAssertTrue(more.waitForNonExistence(timeout: 5))
        XCTAssertTrue(row(25, app).waitUntilStill(), "the open queue kept moving")
        assertNotCovered(row(25, app), app, "with the rest open")
        assertAtLatest(app, "with the rest open")
    }

    func testTappingARowShowsTheWholeMessage() {
        let app = launch()
        app.buttons["queue-tail-read-21"].tapWhenReady()
        XCTAssertTrue(app.navigationBars["Queued message"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["This is the failing screen, match the spacing."].exists)
        record(app, "queued-message-sheet")
        XCTAssertEqual(app.staticTexts["queued-message-hold"].label, "Held: this agent is working")
        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label BEGINSWITH 'Attempts'")).firstMatch.exists)
    }

    func testTheReceiptSitsOnTheComposerAndGoesToTheQueuedRow() {
        let app = launch("many", extra: ["--preview-delivery"])
        let receipt = app.buttons["latest-instruction"]
        XCTAssertTrue(receipt.waitForExistence(timeout: 10))
        assertOnComposer(receipt, app)
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "receipt"
        shot.lifetime = .keepAlways
        add(shot)
        let timeline = app.scrollViews["session-timeline"]
        timeline.swipeDown(velocity: .slow)
        XCTAssertTrue(app.buttons["Jump to latest"].waitForExistence(timeout: 2))
        receipt.tapWhenReady()
        XCTAssertTrue(row(25, app).waitForExistence(timeout: 5))
        XCTAssertTrue(row(25, app).waitUntilStill(), "the receipt's row kept moving")
        assertNotCovered(row(25, app), app, "after the receipt")
        XCTAssertFalse(app.buttons["Jump to latest"].exists)
    }

    func testTheReceiptIsPlainWithNoQueuedRow() {
        let app = launch("none", extra: ["--preview-delivery"])
        let receipt = app.staticTexts["latest-instruction"]
        XCTAssertTrue(receipt.waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["latest-instruction"].exists)
        assertOnComposer(receipt, app)
    }

    private func assertOnComposer(_ receipt: XCUIElement, _ app: XCUIApplication, file: StaticString = #filePath, line: UInt = #line) {
        let composer = app.otherElements["composer"]
        XCTAssertTrue(composer.exists, file: file, line: line)
        XCTAssertTrue(composer.frame.contains(receipt.frame), "the receipt \(receipt.frame) is outside the composer \(composer.frame)", file: file, line: line)
    }

    func testCancellingTheLastRowLeavesTheTranscriptAtItsLatestRow() {
        let app = launch()
        let bubble = row(21, app)
        XCTAssertTrue(bubble.exists)
        app.buttons["queue-tail-menu-21"].tapWhenReady()
        app.buttons["Cancel delivery"].tapWhenReady()
        XCTAssertTrue(bubble.waitForNonExistence(timeout: 5))
        let timeline = app.scrollViews["session-timeline"]
        XCTAssertTrue(timeline.waitUntilRowsStill(), "the transcript kept moving after the last row left")
        assertAtLatest(app, "after the last row left")
        timeline.swipeDown(velocity: .slow)
        XCTAssertTrue(timeline.waitUntilRowsStill(), "the transcript kept moving after the swipe")
        app.buttons["Jump to latest"].tapWhenReady()
        XCTAssertTrue(app.buttons["Jump to latest"].waitForNonExistence(timeout: 3), "Jump didn't land after the last row left")
    }

    func testAFailedReloadFallsBackToTheSnapshotInsteadOfKeepingADeadRow() {
        let app = launch(extra: ["--preview-tail-late", "--preview-tail-fails-after-action"])
        let bubble = row(21, app)
        XCTAssertTrue(bubble.exists, "the first load shows the row")
        app.buttons["queue-tail-menu-21"].tapWhenReady()
        app.buttons["Cancel delivery"].tapWhenReady()
        XCTAssertTrue(bubble.waitForNonExistence(timeout: 5))
        settle()
        XCTAssertFalse(bubble.exists, "the row doesn't come back after the reload failed")
    }

    func testTheTailStaysResponsiveOverAStreamingTranscriptWithAWallpaper() {
        continueAfterFailure = false
        for space in stride(from: 40, through: 280, by: 30) {
            let app = XCUIApplication()
            app.launchArguments = ["--preview-session", "--preview-tail", "-preview-tail", "many", "--preview-theme", "dark", "--preview-stream", "--preview-trailing-space", "\(space)", "--preview-backdrop"]
            app.launch()
            XCTAssertTrue(app.scrollViews["session-timeline"].waitForExistence(timeout: 10))
            let more = app.buttons["queue-tail-more"]
            XCTAssertTrue(more.waitForExistence(timeout: 15))
            app.waitForSteadyJump()
            app.assertJumpHoldsStill("after opening \(space) points short of the end")
            if (space - 40).isMultiple(of: 60) {
                let started = Date()
                more.tap()
                XCTAssertLessThan(Date().timeIntervalSince(started), 5, "a tap took too long at \(space) points short of the end")
                app.waitForSteadyJump()
                app.assertJumpHoldsStill("after opening the queue \(space) points short of the end")
            }
            XCTAssertEqual(app.state, .runningForeground, "the app died at \(space) points short of the end")
            app.terminate()
        }
    }

    func testOpeningTheQueueWhileFollowingKeepsRowsOnScreen() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-tail", "-preview-tail", "many", "--preview-theme", "dark", "--preview-stream"]
        app.launch()
        let timeline = app.scrollViews["session-timeline"]
        XCTAssertTrue(timeline.waitForExistence(timeout: 10))
        let more = app.buttons["queue-tail-more"]
        XCTAssertTrue(more.waitForExistence(timeout: 15))
        app.waitForSteadyJump()
        let viewport = timeline.frame
        let rows = CGRect(x: viewport.minX + 16, y: viewport.minY + 120, width: viewport.width - 32, height: viewport.height * 0.5)
        XCTAssertTrue(ScreenshotInk.shows(app.screenshot().image, in: rows), "the transcript was blank before opening the queue")
        more.tap()
        let end = Date().addingTimeInterval(4)
        var frame = 0
        while Date() < end {
            XCTAssertTrue(ScreenshotInk.shows(app.screenshot().image, in: rows), "the transcript was blank \(frame) frames after opening the queue")
            frame += 1
        }
    }
}
