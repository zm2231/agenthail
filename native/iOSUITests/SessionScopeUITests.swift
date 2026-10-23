import XCTest

@MainActor
final class SessionScopeUITests: XCTestCase {
    func testScopeChipsSwitchWhatTheListShows() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app"]
        app.launch()
        let working = app.buttons["session-computer::demo"]
        let idle = app.buttons["session-computer::codex-demo"]
        XCTAssertTrue(working.waitForExistence(timeout: 10))
        XCTAssertTrue(idle.exists)
        XCTAssertTrue(app.buttons["scope-recent"].isSelected)

        let running = app.buttons["scope-running"]
        XCTAssertTrue(running.label.hasPrefix("Running, "), running.label)
        running.tapWhenReady()
        XCTAssertTrue(running.isSelected)
        XCTAssertTrue(working.waitForExistence(timeout: 5))
        XCTAssertFalse(idle.exists, "Running lists only working sessions")
        XCTAssertTrue(working.value as? String == "Working", "the ring's status is spoken: \(String(describing: working.value))")

        app.buttons["scope-all"].tapWhenReady()
        XCTAssertTrue(idle.waitForExistence(timeout: 5))
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "scope all"
        shot.lifetime = .keepAlways
        add(shot)
    }
}
