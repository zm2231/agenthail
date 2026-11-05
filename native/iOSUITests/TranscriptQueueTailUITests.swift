import XCTest

@MainActor
final class TranscriptQueueTailUITests: XCTestCase {
    private func launch(_ rows: String = "one", theme: String = "dark", extra: [String] = []) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-tail", "-preview-tail", rows, "--preview-theme", theme, "--preview-reading", "--preview-follow"] + extra
        app.launch()
        XCTAssertTrue(app.textViews["composer-input"].waitForExistence(timeout: 15))
        sleep(2)
        return app
    }

    private func settle() { RunLoop.current.run(until: Date().addingTimeInterval(1)) }

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
        settle()
        assertNotCovered(row(21, app), app, "after it arrived late")
        assertAtLatest(app, "after a row arrived late")
    }

    func testARowArrivingDoesNotMoveATranscriptScrolledUp() throws {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-tail", "-preview-tail", "one", "--preview-theme", "dark", "--preview-reading", "--preview-follow", "--preview-tail-late", "--preview-tail-slow"]
        app.launch()
        XCTAssertTrue(app.textViews["composer-input"].waitForExistence(timeout: 15))
        XCTAssertFalse(row(21, app).exists, "the row hasn't arrived yet")
        let timeline = app.scrollViews["session-timeline"]
        timeline.swipeDown(velocity: .slow)
        XCTAssertTrue(app.buttons["Jump to latest"].waitForExistence(timeout: 2))
        let label = try midScreenText(app)
        let before = timeline.staticTexts[label].frame.minY
        sleep(7)
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
        sleep(2)
        app.navigationBars.buttons.element(boundBy: 0).tapWhenReady()
        session.tapWhenReady()
        let timeline = app.scrollViews["session-timeline"]
        XCTAssertTrue(timeline.waitForExistence(timeout: 5), "the cached transcript shows while it reloads")
        timeline.swipeDown(velocity: .slow)
        XCTAssertTrue(app.buttons["Jump to latest"].waitForExistence(timeout: 2))
        let label = try midScreenText(app)
        let before = timeline.staticTexts[label].frame.minY
        sleep(8)
        XCTAssertEqual(timeline.staticTexts[label].frame.minY, before, accuracy: 2, "the landing undid the reader's scroll")
        XCTAssertTrue(app.buttons["Jump to latest"].exists)
    }

    private func open(_ session: XCUIElement, in app: XCUIApplication) {
        if !session.isHittable { app.navigationBars.buttons.element(boundBy: 0).tapWhenReady() }
        session.tapWhenReady(timeout: 15)
        XCTAssertTrue(app.textViews["composer-input"].waitForExistence(timeout: 10))
        sleep(3)
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
        settle()
        XCTAssertTrue(row(21, app).exists, "the tall queue is open")

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
        XCTAssertGreaterThanOrEqual(more.frame.height, 44)
        XCTAssertFalse(row(21, app).exists)
        assertNotCovered(row(25, app), app, "with the rest collapsed")
        assertAtLatest(app, "with the rest collapsed")
        more.tap()
        settle()
        XCTAssertFalse(more.exists)
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

    func testTheReceiptGoesToTheQueuedRow() {
        let app = launch("many", extra: ["--preview-delivery"])
        let timeline = app.scrollViews["session-timeline"]
        timeline.swipeDown(velocity: .slow)
        XCTAssertTrue(app.buttons["Jump to latest"].waitForExistence(timeout: 2))
        let receipt = app.buttons["latest-instruction"]
        XCTAssertTrue(receipt.exists)
        receipt.tapWhenReady()
        XCTAssertTrue(row(25, app).waitForExistence(timeout: 5))
        settle()
        assertNotCovered(row(25, app), app, "after the receipt")
        XCTAssertFalse(app.buttons["Jump to latest"].exists)
    }

    func testTheReceiptIsPlainWithNoQueuedRow() {
        let app = launch("none", extra: ["--preview-delivery"])
        XCTAssertTrue(app.staticTexts["latest-instruction"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["latest-instruction"].exists)
    }

    func testAPeerRowDoesNotPushOutThePersonsOwn() {
        let app = launch("peer")
        XCTAssertTrue(app.staticTexts["Queued · From @helper"].exists, "a peer row names its sender briefly")
        XCTAssertTrue(row(21, app).exists, "the person's own row is one of the three")
        XCTAssertEqual(app.buttons["queue-tail-more"].label, "2 more queued")
        XCTAssertFalse(app.buttons["queue-tail-steer-25"].exists, "a peer row can't steer as the person")
    }

    func testAFailedRowKeepsItsReasonAndRetry() {
        let app = launch("dead")
        XCTAssertTrue(app.staticTexts["queue-tail-reason-21"].exists)
        XCTAssertTrue(app.buttons["queue-tail-retry-21"].exists)
        XCTAssertTrue(app.buttons["queue-tail-dismiss-21"].exists)
    }

    func testCancellingTheLastRowLeavesTheTranscriptAtItsLatestRow() {
        let app = launch()
        let bubble = row(21, app)
        XCTAssertTrue(bubble.exists)
        app.buttons["queue-tail-menu-21"].tapWhenReady()
        app.buttons["Cancel delivery"].tapWhenReady()
        XCTAssertTrue(bubble.waitForNonExistence(timeout: 5))
        settle()
        assertAtLatest(app, "after the last row left")
        let timeline = app.scrollViews["session-timeline"]
        timeline.swipeDown(velocity: .slow)
        settle()
        app.buttons["Jump to latest"].tapWhenReady()
        XCTAssertTrue(app.buttons["Jump to latest"].waitForNonExistence(timeout: 3), "Jump didn't land after the last row left")
    }

    func testAFailedReloadFallsBackToTheSnapshotInsteadOfKeepingADeadRow() {
        let app = launch(extra: ["--preview-tail-late", "--preview-tail-fails-after-action"])
        settle()
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
        for space in stride(from: 40, through: 280, by: 60) {
            let app = XCUIApplication()
            app.launchArguments = ["--preview-session", "--preview-tail", "-preview-tail", "many", "--preview-theme", "dark", "--preview-stream", "--preview-trailing-space", "\(space)", "--preview-backdrop"]
            app.launch()
            let more = app.buttons["queue-tail-more"]
            XCTAssertTrue(more.waitForExistence(timeout: 15))
            sleep(4)
            let started = Date()
            more.tap()
            XCTAssertLessThan(Date().timeIntervalSince(started), 5, "a tap took too long at \(space) points short of the end")
            sleep(2)
            let jump = app.buttons["Jump to latest"]
            let settled = jump.exists
            for _ in 0..<8 {
                RunLoop.current.run(until: Date().addingTimeInterval(0.15))
                XCTAssertEqual(jump.exists, settled, "Jump to latest flapped at \(space)")
            }
            XCTAssertEqual(app.state, .runningForeground, "the app died at \(space) points short of the end")
            app.terminate()
        }
    }

    func testShots() {
        for theme in ["dark", "light"] {
            for rows in ["one", "many", "peer", "dead"] {
                let app = launch(rows, theme: theme)
                record(app, "tail-\(rows)-\(theme)")
                app.terminate()
            }
        }
    }
}
