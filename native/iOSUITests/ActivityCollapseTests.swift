import XCTest

@MainActor
final class ActivityCollapseTests: XCTestCase {
    func testCollapsingALongRunFromItsEndKeepsTheRunOnScreen() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-long-activity"]
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
        collapse.tapWhenStill()
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: collapse)], timeout: 5), .completed)
        // Give the scroll view time to settle before reading what it shows.
        RunLoop.current.run(until: Date().addingTimeInterval(1))
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = "After collapsing the run from its end"
        attachment.lifetime = .keepAlways
        add(attachment)
        XCTAssertTrue(activity.isHittable, "the collapsed run's header should be on screen, at \(activity.frame) in \(timeline.frame)")
        let reply = app.staticTexts["Every target builds after reconnecting."]
        XCTAssertTrue(reply.isHittable, "the reply after the run should be on screen, at \(reply.frame) in \(timeline.frame)")
    }

    func testCollapsingAToolFromItsEndKeepsTheToolOnScreen() {
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
        // The failed tool above opens on its own and can push this line under the composer, where a tap
        // reaches the composer even though the line reports itself hittable.
        for _ in 0..<6 where tool.frame.maxY > timeline.frame.maxY - 200 { timeline.swipeUp() }
        tool.tapWhenStill()
        let collapse = app.buttons["collapse-tool-run-2"]
        for _ in 0..<6 where !collapse.isHittable { timeline.swipeUp() }
        if collapse.frame.maxY > timeline.frame.maxY - 140 { timeline.swipeUp() }
        XCTAssertTrue(collapse.isHittable)
        XCTAssertFalse(tool.isHittable, "the tool's line should have scrolled above the screen before collapsing")
        collapse.tapWhenStill()
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: collapse)], timeout: 5), .completed)
        RunLoop.current.run(until: Date().addingTimeInterval(1))
        XCTAssertTrue(tool.isHittable && tool.frame.maxY < timeline.frame.maxY - 140, "the collapsed tool's line should be on screen above the composer, at \(tool.frame) in \(timeline.frame)")
    }
}
