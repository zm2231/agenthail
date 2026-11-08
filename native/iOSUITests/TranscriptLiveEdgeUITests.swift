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

    private func waitForReplies(_ app: XCUIApplication, timeout: TimeInterval = 10) -> Int {
        let deadline = Date().addingTimeInterval(timeout)
        var last = newestReply(app)
        while Date() < deadline {
            RunLoop.current.run(until: Date().addingTimeInterval(0.3))
            let next = newestReply(app)
            if next > 0, next == last { return next }
            last = next
        }
        return last
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

    private func attachReplyProbe(_ app: XCUIApplication, reply: Int, viewport: CGRect, tree: [XCUIElementSnapshot]) {
        let needle = "of reply \(reply)."
        let anywhere = app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", needle))
        var lines = [
            "reply \(reply) in timeline tree: \(tree.contains { $0.label.contains(needle) })",
            "reply \(reply) anywhere in app: \(anywhere.count) at \(anywhere.allElementsBoundByIndex.map { "\($0.frame)" })",
            "timeline frame: \(viewport)",
            "keyboard frame: \(app.keyboards.firstMatch.exists ? "\(app.keyboards.firstMatch.frame)" : "none")",
            "timeline tree (\(tree.count) elements):",
        ]
        lines += tree.map { "\($0.elementType.rawValue) id=\($0.identifier) frame=\($0.frame) label=\($0.label.prefix(80))" }
        let probe = XCTAttachment(string: lines.joined(separator: "\n"))
        probe.name = "reply-probe"
        probe.lifetime = .keepAlways
        add(probe)
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
        let left = waitForReplies(app)
        XCTAssertGreaterThan(left, 0, "the session showed no reply before switching away")
        app.backToSessions()
        app.buttons["session-computer::codex-demo"].tapWhenReady()
        XCTAssertTrue(app.buttons["Session menu"].waitForExistence(timeout: 10))
        sleep(25)
        app.backToSessions()
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

    func testJumpingToLatestKeepsTheDraftFocusAndOpenRows() { keepsDraftFocus() }

    func testRepeatJumpKeepsDraftFocusTenTimes() { repeatTen("keeps draft focus (:99)", keepsDraftFocus) }

    func testRepeatJumpWhileStreamingTenTimes() { repeatTen("while streaming (:119)", streamingJump) }

    private func repeatTen(_ name: String, _ body: () -> Void) {
        var failed: [Int] = []
        for run in 1...10 {
            let before = testRun?.failureCount ?? 0
            body()
            XCUIApplication().terminate()
            if (testRun?.failureCount ?? 0) > before { failed.append(run) }
        }
        let text = "\(name): \(failed.count) of 10 runs failed, runs \(failed)"
        print("REPEAT-RESULT \(text)")
        let attachment = XCTAttachment(string: text)
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }

    private func keepsDraftFocus() {
        let app = launch(streaming: false)
        app.buttons[sessionID].tapWhenReady(timeout: 15)
        let timeline = app.scrollViews["session-timeline"]
        XCTAssertTrue(timeline.waitForExistence(timeout: 10))
        let latest = waitForReplies(app)
        XCTAssertGreaterThan(latest, 0, "the session opened without a reply on screen")
        let middle = timeline.frame.insetBy(dx: 0, dy: timeline.frame.height * 0.25)
        var midScreenTool: String?
        for _ in 0..<8 where midScreenTool == nil {
            timeline.swipeDown(velocity: .slow)
            midScreenTool = elements(app).filter({ $0.identifier.hasPrefix("tool-call-") && middle.contains(CGPoint(x: $0.frame.midX, y: $0.frame.midY)) }).map(\.identifier).first
        }
        guard let toolID = midScreenTool else { return XCTFail("no tool row mid-screen") }
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
        let tree = elements(app)
        attachReplyProbe(app, reply: latest, viewport: viewport, tree: tree)
        XCTAssertTrue(tree.contains { $0.label.contains("of reply \(latest).") && viewport.intersects($0.frame) }, "the latest reply \(latest) is not on screen after Jump to latest")
        XCTAssertTrue((composer.value as? String ?? "").contains("draft kept"), "the draft was lost on Jump to latest")
        XCTAssertEqual(app.keyboards.firstMatch.exists, focused, "Jump to latest changed the composer's focus")
        let row = timeline.buttons[toolID]
        for _ in 0..<4 where !row.exists { timeline.swipeDown(velocity: .slow) }
        XCTAssertEqual(row.value as? String, "Expanded", "an open tool row closed on Jump to latest")
    }

    func testJumpingToLatestWhileStreamingShowsTheLatestRows() { streamingJump() }

    private func streamingJump() {
        let app = launch(direct: captured)
        if !captured { app.buttons[sessionID].tapWhenReady(timeout: 15) }
        let timeline = app.scrollViews["session-timeline"]
        XCTAssertTrue(timeline.waitForExistence(timeout: 10))
        XCTAssertGreaterThan(waitForReplies(app), 0, "the session opened without a reply on screen")
        for _ in 0..<12 { timeline.swipeDown(velocity: .fast) }
        XCTAssertTrue(timeline.waitUntilRowsStill(), "the transcript kept moving after the swipes")
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

    func testJumpToLatestFloatsAboveTheComposerWithTheKeyboardUp() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-reading", "--preview-reading-top", "--preview-follow", "--preview-theme", "dark"]
        app.launch()
        let jump = app.buttons["Jump to latest"]
        XCTAssertTrue(jump.waitForExistence(timeout: 10), "a session opened at its oldest row offered no way to the latest")
        let composer = app.textViews["Message to this agent"]
        composer.tapWhenReady()
        composer.typeText(" ")
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5))
        XCTAssertLessThanOrEqual(jump.frame.maxY, composer.frame.minY, "Jump to latest covered the composer")
        XCTAssertTrue(jump.isHittable)
        jump.tap()
        let gone = NSPredicate(format: "exists == false")
        wait(for: [XCTNSPredicateExpectation(predicate: gone, object: jump)], timeout: 5)
    }

    func testReopeningALongSessionLeavesTheTranscriptStill() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-follow", "--preview-theme", "dark"]
            + (captured ? ["--preview-capture"] : ["--preview-reading"]) + ["--preview-backdrop"]
        app.launch()
        for round in 1...3 {
            app.buttons[sessionID].tapWhenReady(timeout: 15)
            XCTAssertTrue(app.scrollViews["session-timeline"].waitForExistence(timeout: 10))
            XCTAssertTrue(app.textViews["composer-input"].waitForExistence(timeout: 10), "the session did not load, round \(round)")
            app.waitForSteadyJump()
            app.assertJumpHoldsStill("after opening the session, round \(round)")
            app.backToSessions()
        }
    }
}
