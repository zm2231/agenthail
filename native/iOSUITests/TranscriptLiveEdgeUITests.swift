import XCTest

@MainActor
final class TranscriptLiveEdgeUITests: XCTestCase {
    private var captured: Bool { ProcessInfo.processInfo.environment["AGENTHAIL_PROBE_CAPTURE"] == "1" }
    private var sessionID: String { "session-computer::" + (ProcessInfo.processInfo.environment["AGENTHAIL_PROBE_SESSION"] ?? "demo") }

    private func launch(direct: Bool = false, streaming: Bool = true) -> XCUIApplication {
        let app = XCUIApplication()
        let stream = ProcessInfo.processInfo.environment["AGENTHAIL_PROBE_NOSTREAM"] == "1" ? ["--preview-follow"] : ["--preview-stream"] + (streaming ? [] : ["--preview-stream-paused"])
        app.launchArguments = ["--preview-session", "--preview-theme", "dark"] + stream + (direct ? [] : ["--preview-app"]) + (captured ? ["--preview-capture"] : [])
        app.launch()
        return app
    }

    @discardableResult
    private func record(_ app: XCUIApplication, _ name: String) -> UIImage {
        let screenshot = app.screenshot()
        let shot = XCTAttachment(screenshot: screenshot)
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
        return screenshot.image
    }

    private func elements(_ app: XCUIApplication) -> [XCUIElementSnapshot] {
        guard let root = try? app.scrollViews["session-timeline"].snapshot() else { return [] }
        var all: [XCUIElementSnapshot] = []
        var pending = [root]
        while let next = pending.popLast() {
            all.append(next)
            pending += next.children
        }
        return all
    }

    private func newestReply(_ app: XCUIApplication) -> Int {
        elements(app).compactMap { element in
            element.label.components(separatedBy: "of reply ").dropFirst().first.flatMap { Int($0.prefix { $0.isNumber }) }
        }.max() ?? 0
    }

    func testReturningToASessionAfterAnotherLandsAtTheLiveEdge() {
        let app = launch()
        let session = app.buttons[sessionID]
        session.tapWhenReady(timeout: 15)
        XCTAssertTrue(app.buttons["Session menu"].waitForExistence(timeout: 10))
        sleep(3)
        let left = newestReply(app)
        XCTAssertGreaterThan(left, 0, "the session showed no reply before switching away")
        app.navigationBars.buttons.element(boundBy: 0).tapWhenReady()
        app.buttons["session-computer::codex-demo"].tapWhenReady()
        XCTAssertTrue(app.buttons["Session menu"].waitForExistence(timeout: 10))
        sleep(25)
        app.navigationBars.buttons.element(boundBy: 0).tapWhenReady()
        session.tapWhenReady()
        XCTAssertTrue(app.scrollViews["session-timeline"].waitForExistence(timeout: 5))
        var seen: [(reply: Int, at: Date)] = []
        let end = Date().addingTimeInterval(2.5)
        while Date() < end {
            let newest = newestReply(app)
            if newest > 0, seen.last?.reply != newest { seen.append((newest, Date())) }
            record(app, "return-\(seen.count)")
        }
        XCTAssertGreaterThan(seen.first?.reply ?? 0, left, "the returned session did not show the replies it caught up on")
        let opening = seen.filter { $0.at.timeIntervalSince(seen.first?.at ?? $0.at) < 2 }
        XCTAssertLessThanOrEqual(opening.count, 2, "the transcript walked through caught-up replies \(opening.map(\.reply)) faster than the agent writes one every 3.6 s")
        XCTAssertFalse(app.buttons["Jump to latest"].exists, "a returned session opened away from its latest row")
    }

    func testJumpingToLatestKeepsTheDraftFocusAndOpenRows() {
        let app = launch(streaming: false)
        app.buttons[sessionID].tapWhenReady(timeout: 15)
        let timeline = app.scrollViews["session-timeline"]
        XCTAssertTrue(timeline.waitForExistence(timeout: 10))
        sleep(2)
        let latest = newestReply(app)
        XCTAssertGreaterThan(latest, 0, "the session opened without a reply on screen")
        for _ in 0..<2 { timeline.swipeDown(velocity: .slow) }
        let middle = timeline.frame.insetBy(dx: 0, dy: timeline.frame.height * 0.25)
        guard let toolID = elements(app).filter({ $0.identifier.hasPrefix("tool-call-") && middle.contains(CGPoint(x: $0.frame.midX, y: $0.frame.midY)) }).map(\.identifier).first
        else { return XCTFail("no tool row mid-screen") }
        let tool = timeline.buttons[toolID]
        tool.tap()
        expectation(for: NSPredicate(format: "value == 'Expanded'"), evaluatedWith: tool)
        waitForExpectations(timeout: 5)
        let composer = app.textViews["composer-input"]
        composer.tap()
        composer.typeText(" draft kept")
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5))
        let focused = app.keyboards.firstMatch.exists
        app.buttons["Jump to latest"].tapWhenReady()
        XCTAssertTrue(app.buttons["Jump to latest"].waitForNonExistence(timeout: 3), "Jump to latest did not reach the latest row")
        record(app, "after-jump")
        let viewport = timeline.frame
        XCTAssertTrue(elements(app).contains { $0.label.contains("of reply \(latest).") && viewport.intersects($0.frame) }, "the latest reply \(latest) is not on screen after Jump to latest")
        XCTAssertTrue((composer.value as? String ?? "").contains("draft kept"), "the draft was lost on Jump to latest")
        XCTAssertEqual(app.keyboards.firstMatch.exists, focused, "Jump to latest changed the composer's focus")
        let row = timeline.buttons[toolID]
        for _ in 0..<4 where !row.exists { timeline.swipeDown(velocity: .slow) }
        XCTAssertEqual(row.value as? String, "Expanded", "an open tool row closed on Jump to latest")
    }

    func testJumpingToLatestWhileStreamingShowsTheLatestRows() {
        let app = launch(direct: captured)
        if !captured { app.buttons[sessionID].tapWhenReady(timeout: 15) }
        let timeline = app.scrollViews["session-timeline"]
        XCTAssertTrue(timeline.waitForExistence(timeout: 10))
        sleep(3)
        for _ in 0..<12 { timeline.swipeDown(velocity: .fast) }
        sleep(2)
        let viewport = timeline.frame
        let rows = CGRect(x: viewport.minX + 16, y: viewport.minY + 200, width: viewport.width - 32, height: viewport.height * 0.4)
        XCTAssertTrue(ScreenshotInk.shows(record(app, "before-jump"), in: rows), "the transcript was blank before the jump")
        app.buttons["Jump to latest"].tapWhenReady()
        let end = Date().addingTimeInterval(3)
        var frame = 0
        while Date() < end {
            XCTAssertTrue(ScreenshotInk.shows(record(app, "jump-\(frame)"), in: rows), "the transcript was blank \(frame) frames after Jump to latest")
            frame += 1
        }
    }

    private func assertStill(_ app: XCUIApplication, _ context: String) {
        let jump = app.buttons["Jump to latest"]
        let first = jump.exists
        for _ in 0..<8 {
            RunLoop.current.run(until: Date().addingTimeInterval(0.15))
            XCTAssertEqual(jump.exists, first, "Jump to latest kept appearing and disappearing \(context)")
        }
    }

    func testReopeningALongSessionLeavesTheTranscriptStill() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-follow", "--preview-theme", "dark"]
            + (captured ? ["--preview-capture"] : ["--preview-reading"]) + ["--preview-backdrop"]
        app.launch()
        for round in 1...3 {
            app.buttons[sessionID].tapWhenReady(timeout: 15)
            XCTAssertTrue(app.scrollViews["session-timeline"].waitForExistence(timeout: 10))
            sleep(5)
            assertStill(app, "after opening the session, round \(round)")
            app.navigationBars.buttons.element(boundBy: 0).tapWhenReady()
        }
    }
}
