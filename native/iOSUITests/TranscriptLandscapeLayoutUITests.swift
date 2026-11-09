import XCTest

@MainActor
final class TranscriptLandscapeLayoutUITests: XCTestCase {
    private func open(landscape: Bool) -> XCUIApplication {
        if landscape {
            XCUIDevice.shared.orientation = .landscapeLeft
            addTeardownBlock { @MainActor in XCUIDevice.shared.orientation = .portrait }
        }
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-reading", "--preview-theme", "dark"]
        app.launch()
        let timeline = app.scrollViews["session-timeline"]
        XCTAssertTrue(timeline.waitForExistence(timeout: 10))
        XCTAssertTrue(timeline.waitUntilRowsStill())
        return app
    }

    private func headerBottom(_ app: XCUIApplication) -> CGFloat {
        let working = app.staticTexts["Working"]
        return max(app.navigationBars.firstMatch.frame.maxY, working.exists ? working.frame.maxY : 0)
    }

    private func composerTop(_ app: XCUIApplication) -> CGFloat {
        app.otherElements["composer"].frame.minY
    }

    // XCUIElement swipes miss the transcript in landscape; a drag at window coordinates reaches it.
    private func drag(_ app: XCUIApplication, by distance: CGFloat) {
        let window = app.windows.firstMatch
        let middle = (headerBottom(app) + composerTop(app)) / 2
        let start = window.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: window.frame.midX, dy: middle - distance / 2))
        start.press(forDuration: 0.05, thenDragTo: start.withOffset(CGVector(dx: 0, dy: distance)), withVelocity: 1500, thenHoldForDuration: 0.05)
    }

    private func rows(_ app: XCUIApplication) throws -> [XCUIElementSnapshot] {
        var found: [XCUIElementSnapshot] = []
        var pending = [try app.scrollViews["session-timeline"].snapshot()]
        while let next = pending.popLast() {
            pending += next.children
            if [.staticText, .button].contains(next.elementType), next.frame.height > 8 { found.append(next) }
        }
        return found
    }

    private func assertFirstRowRestsBelowTheHeader(_ app: XCUIApplication, _ context: String) throws {
        for _ in 0..<20 { drag(app, by: 120) }
        XCTAssertTrue(app.scrollViews["session-timeline"].waitUntilRowsStill())
        let header = headerBottom(app)
        let first = try rows(app).filter { $0.frame.maxY > app.navigationBars.firstMatch.frame.maxY }.min { $0.frame.minY < $1.frame.minY }
        let top = try XCTUnwrap(first, "no row on screen at the top \(context)")
        XCTAssertGreaterThanOrEqual(top.frame.minY, header - 0.5, "at the top the first row '\(top.label.prefix(40))' starts behind the header \(context)")
    }

    func testLandscapeKeepsHalfTheHeightForTheTranscriptAndARowAboveTheQueue() throws {
        let app = open(landscape: true)
        let height = app.windows.firstMatch.frame.height
        let header = headerBottom(app), composer = composerTop(app)
        XCTAssertGreaterThanOrEqual(composer - header, height / 2, "the transcript band is \(composer - header) pt of \(height)")

        let tail = app.descendants(matching: .any).matching(NSPredicate(format: "identifier MATCHES 'queue-computer/[0-9]+'"))
        XCTAssertGreaterThanOrEqual(tail.count, 2)
        let queued = tail.allElementsBoundByIndex.map(\.frame)
        let above = try rows(app).filter { row in
            row.frame.minY >= header && row.frame.maxY <= composer && !queued.contains { $0.intersects(row.frame) }
        }
        XCTAssertFalse(above.isEmpty, "at the latest row no transcript row shows above the queued ones")

        XCTAssertTrue(app.buttons["queue-tail-retry-2"].isHittable, "Send again stays on the one-line delivery row")
        XCTAssertTrue(app.buttons["queue-tail-dismiss-2"].isHittable, "Dismiss stays on the one-line delivery row")
        XCTAssertFalse(app.descendants(matching: .any)["queue-tail-reason-2"].exists, "the reason moves into the sheet")
        XCTAssertTrue(app.descendants(matching: .any)["composer-turn-settings"].exists, "one chip keeps the turn settings in view")
        XCTAssertFalse(app.descendants(matching: .any)["composer-effort"].exists, "the chip row folds into the one chip")
        XCTAssertTrue(app.descendants(matching: .any)["session-summary"].exists, "the status stays in the header")

        let shot = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        shot.name = "landscape at the latest row"
        shot.lifetime = .keepAlways
        add(shot)

        app.buttons["queue-tail-read-2"].tap()
        XCTAssertTrue(app.descendants(matching: .any)["queued-message-reason"].waitForExistence(timeout: 5), "the sheet shows the delivery reason")
        app.buttons["Done"].tap()

        try assertFirstRowRestsBelowTheHeader(app, "in landscape")
    }

    func testPortraitKeepsItsLayoutAndRestsBelowTheHeaderAtTheTop() throws {
        let app = open(landscape: false)
        XCTAssertTrue(app.descendants(matching: .any)["composer-effort"].exists, "portrait keeps the chip row")
        XCTAssertFalse(app.descendants(matching: .any)["composer-turn-settings"].exists)
        XCTAssertTrue(app.descendants(matching: .any)["queue-tail-reason-2"].exists, "portrait keeps the reason on the row")
        try assertFirstRowRestsBelowTheHeader(app, "in portrait")
    }
}
