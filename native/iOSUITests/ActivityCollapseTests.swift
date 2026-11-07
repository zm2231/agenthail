import XCTest

@MainActor
final class ActivityCollapseTests: XCTestCase {
    func testCollapsingALongRunFromItsEndKeepsTheRunOnScreen() {
        collapseLongRunFromItsEnd([])
    }

    func testCollapsingALongRunBelowALongHistoryKeepsTheRunOnScreen() {
        collapseLongRunFromItsEnd(["--preview-long-history"])
    }

    private func collapseLongRunFromItsEnd(_ extra: [String]) {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-long-activity"] + extra
        app.launch()
        let timeline = app.scrollViews["session-timeline"]
        XCTAssertTrue(timeline.waitForExistence(timeout: 10))
        let activity = app.buttons["activity-3"]
        XCTAssertTrue(activity.waitForExistence(timeout: 10))
        for _ in 0..<6 where !activity.isHittable { timeline.swipeDown() }
        activity.tapWhenReady()
        let collapse = app.buttons["collapse-activity-3"]
        for _ in 0..<12 where !collapse.isHittable { timeline.swipeUp() }
        if collapse.frame.maxY > timeline.frame.maxY - 140 { timeline.swipeUp() }
        XCTAssertTrue(collapse.isHittable)
        XCTAssertGreaterThanOrEqual(collapse.frame.height, 44, "the control should be a full-size tap target, at \(collapse.frame)")
        XCTAssertGreaterThan(collapse.frame.width, timeline.frame.width / 2, "the control should span the row, at \(collapse.frame)")
        collapse.tapWhenStill()
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: collapse)], timeout: 5), .completed)
        let landed = activity.frame.minY
        // Give the scroll view time to settle before reading what it shows.
        RunLoop.current.run(until: Date().addingTimeInterval(1))
        XCTAssertLessThan(abs(activity.frame.minY - landed), 8, "the collapsed run's header moved from \(landed) to \(activity.frame.minY) after the reveal landed")
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = "After collapsing the run from its end"
        attachment.lifetime = .keepAlways
        add(attachment)
        XCTAssertTrue(activity.isHittable, "the collapsed run's header should be on screen, at \(activity.frame) in \(timeline.frame)")
        let reply = app.staticTexts["Every target builds after reconnecting."]
        XCTAssertTrue(reply.isHittable, "the reply after the run should be on screen, at \(reply.frame) in \(timeline.frame)")
    }

    func testCollapsingAToolFromItsEndKeepsTheToolOnScreen() {
        if UIDevice.current.userInterfaceIdiom == .pad {
            // A portrait iPad shows the whole expanded tool, so its line never scrolls away.
            XCUIDevice.shared.orientation = .landscapeLeft
            addTeardownBlock { @MainActor in XCUIDevice.shared.orientation = .portrait }
        }
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-long-activity"]
        app.launch()
        let timeline = app.scrollViews["session-timeline"]
        XCTAssertTrue(timeline.waitForExistence(timeout: 10))
        let activity = app.buttons["activity-3"]
        XCTAssertTrue(activity.waitForExistence(timeout: 10))
        for _ in 0..<6 where !activity.isHittable { timeline.swipeDown() }
        activity.tapWhenReady()
        let tool = app.buttons["tool-run-2"]
        XCTAssertTrue(tool.waitForExistence(timeout: 5))
        tool.scrollIntoMiddle(of: timeline)
        tool.tapWhenStill()
        let collapse = app.buttons["collapse-tool-run-2"]
        for _ in 0..<6 where !collapse.isHittable { timeline.swipeUp() }
        if collapse.frame.maxY > timeline.frame.maxY - 140 { timeline.swipeUp() }
        XCTAssertTrue(collapse.isHittable)
        let top = app.navigationBars.firstMatch.frame.maxY
        XCTAssertLessThanOrEqual(tool.frame.maxY, top, "the tool's line should have scrolled under the top bar before collapsing, at \(tool.frame)")
        XCTAssertGreaterThanOrEqual(collapse.frame.height, 44, "the control should be a full-size tap target, at \(collapse.frame)")
        collapse.tapWhenStill()
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: collapse)], timeout: 5), .completed)
        RunLoop.current.run(until: Date().addingTimeInterval(1))
        XCTAssertTrue(tool.frame.minY >= top && tool.frame.maxY < timeline.frame.maxY - 140, "the collapsed tool's line should be on screen between the top bar and the composer, at \(tool.frame) under \(top) in \(timeline.frame)")
    }
}
