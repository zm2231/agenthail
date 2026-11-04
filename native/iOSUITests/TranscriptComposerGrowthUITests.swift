import XCTest

@MainActor
final class TranscriptComposerGrowthUITests: XCTestCase {
    private func launch(rows: Int = 4, extra: [String] = ["--preview-reading", "--preview-follow"]) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-session-queue", "-preview-session-queue-rows", String(rows), "--preview-theme", "dark"] + extra
        app.launch()
        XCTAssertTrue(app.buttons["session-queue-header"].waitForExistence(timeout: 10))
        sleep(1)
        return app
    }

    private func settle() { RunLoop.current.run(until: Date().addingTimeInterval(1)) }

    private func assertAtLatest(_ app: XCUIApplication, _ context: String, line: UInt = #line) {
        let jump = app.buttons["Jump to latest"]
        for _ in 0..<8 {
            RunLoop.current.run(until: Date().addingTimeInterval(0.15))
            XCTAssertFalse(jump.exists, "the transcript left its latest row \(context)", line: line)
        }
    }

    private func midScreenRow(_ app: XCUIApplication) throws -> String {
        let timeline = app.scrollViews["session-timeline"]
        let band = timeline.frame.insetBy(dx: 0, dy: timeline.frame.height * 0.2)
        var pending = [try timeline.snapshot()]
        while let next = pending.popLast() {
            if next.elementType == .staticText, !next.label.isEmpty, band.contains(CGPoint(x: next.frame.midX, y: next.frame.midY)) { return next.label }
            pending += next.children
        }
        throw NSError(domain: "TranscriptComposerGrowthUITests", code: 1, userInfo: [NSLocalizedDescriptionKey: "no text mid-screen"])
    }

    private func top(of label: String, _ app: XCUIApplication) -> CGFloat {
        app.scrollViews["session-timeline"].staticTexts[label].frame.minY
    }

    private func scrollUp(_ app: XCUIApplication) throws -> (label: String, top: CGFloat) {
        let timeline = app.scrollViews["session-timeline"]
        timeline.swipeDown(velocity: .slow)
        settle()
        XCTAssertTrue(app.buttons["Jump to latest"].waitForExistence(timeout: 5), "the transcript didn't scroll away from its latest row")
        let label = try midScreenRow(app)
        return (label, top(of: label, app))
    }

    private func raiseKeyboard(_ app: XCUIApplication) {
        app.textViews["composer-input"].tapWhenReady()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5))
        settle()
    }

    func testTheKeyboardKeepsAFollowingTranscriptAtItsLatestRow() {
        let app = launch()
        assertAtLatest(app, "after opening")
        raiseKeyboard(app)
        assertAtLatest(app, "with the keyboard up")
        app.buttons["Session menu"].tapWhenReady()
        app.buttons["Session details"].tapWhenReady()
        let details = app.navigationBars["Session details"]
        XCTAssertTrue(details.waitForExistence(timeout: 5))
        XCTAssertFalse(app.keyboards.firstMatch.exists, "the sheet lowers the keyboard")
        details.buttons["Done"].tapWhenReady()
        XCTAssertTrue(details.waitForNonExistence(timeout: 5))
        settle()
        assertAtLatest(app, "after the keyboard went down and came back")
    }

    func testAComposerGrowingWithTypedLinesKeepsAFollowingTranscriptAtItsLatestRow() {
        let app = launch()
        raiseKeyboard(app)
        let composer = app.textViews["composer-input"]
        let before = composer.frame.height
        composer.typeText("\none\ntwo\nthree\nfour")
        settle()
        XCTAssertGreaterThan(composer.frame.height, before + 40, "the composer didn't grow")
        assertAtLatest(app, "after the composer grew with typed lines")
    }

    func testTheKeyboardDoesNotMoveATranscriptThePersonScrolledUp() throws {
        let app = launch()
        let row = try scrollUp(app)
        raiseKeyboard(app)
        XCTAssertEqual(top(of: row.label, app), row.top, accuracy: 2, "the keyboard moved a transcript scrolled up")
        app.buttons["Session menu"].tapWhenReady()
        app.buttons["Session details"].tapWhenReady()
        let details = app.navigationBars["Session details"]
        XCTAssertTrue(details.waitForExistence(timeout: 5))
        XCTAssertFalse(app.keyboards.firstMatch.exists, "the sheet lowers the keyboard")
        details.buttons["Done"].tapWhenReady()
        XCTAssertTrue(details.waitForNonExistence(timeout: 5))
        settle()
        XCTAssertEqual(top(of: row.label, app), row.top, accuracy: 2, "lowering and raising the keyboard moved a transcript scrolled up")
        XCTAssertTrue(app.buttons["Jump to latest"].exists)
    }

    func testTheQueueSectionDoesNotMoveATranscriptThePersonScrolledUp() throws {
        let app = launch()
        let row = try scrollUp(app)
        let header = app.buttons["session-queue-header"]
        header.tap()
        XCTAssertTrue(app.buttons["session-queue-menu-11"].waitForExistence(timeout: 5))
        settle()
        XCTAssertEqual(top(of: row.label, app), row.top, accuracy: 2, "opening the section moved a transcript scrolled up")
        header.tap()
        settle()
        XCTAssertFalse(app.buttons["session-queue-menu-11"].exists)
        XCTAssertEqual(top(of: row.label, app), row.top, accuracy: 2, "collapsing the section moved a transcript scrolled up")
    }

    func testTheQueueSectionKeepsAFollowingTranscriptAtItsLatestRow() {
        let app = launch()
        let header = app.buttons["session-queue-header"]
        header.tap()
        XCTAssertTrue(app.buttons["session-queue-menu-11"].waitForExistence(timeout: 5))
        settle()
        assertAtLatest(app, "with the section open")
        header.tap()
        settle()
        assertAtLatest(app, "with the section collapsed")
    }

    func testARowTheSnapshotDidNotHaveAppearsWithoutLeavingTheLatestRow() {
        let app = launch(rows: 2, extra: ["--preview-reading", "--preview-follow", "--preview-queue-not-in-snapshot"])
        settle()
        XCTAssertTrue(app.buttons["session-queue-menu-11"].exists)
        assertAtLatest(app, "after a row arrived late")
    }

    func testAFailedReloadFallsBackToTheSnapshotInsteadOfKeepingADeadRow() {
        let app = launch(rows: 2, extra: ["--preview-reading", "--preview-follow", "--preview-queue-not-in-snapshot", "--preview-queue-fails-after-action"])
        let menu = app.buttons["session-queue-menu-11"]
        XCTAssertTrue(menu.waitForExistence(timeout: 5), "the first load shows the row")
        menu.tap()
        app.buttons["Cancel delivery"].tapWhenReady()
        XCTAssertTrue(menu.waitForNonExistence(timeout: 5), "a row the snapshot doesn't have stayed after a failed reload")
        XCTAssertFalse(app.buttons["session-queue-header"].exists)
    }

    func testTheSectionStaysResponsiveOverAStreamingTranscriptWithAWallpaper() {
        continueAfterFailure = false
        for space in stride(from: 40, through: 280, by: 60) {
            let app = launch(rows: 2, extra: ["--preview-stream", "--preview-trailing-space", "\(space)", "--preview-backdrop"])
            sleep(4)
            let jump = app.buttons["Jump to latest"]
            let header = app.buttons["session-queue-header"]
            for _ in 0..<6 {
                let started = Date()
                header.tap()
                XCTAssertLessThan(Date().timeIntervalSince(started), 5, "a tap took too long at \(space) points short of the end")
                RunLoop.current.run(until: Date().addingTimeInterval(0.4))
            }
            header.tap()
            sleep(2)
            let settled = jump.exists
            for _ in 0..<8 {
                RunLoop.current.run(until: Date().addingTimeInterval(0.15))
                XCTAssertEqual(jump.exists, settled, "Jump to latest flapped at \(space)")
            }
            XCTAssertEqual(app.state, .runningForeground, "the app died at \(space) points short of the end")
            app.terminate()
        }
    }
}
