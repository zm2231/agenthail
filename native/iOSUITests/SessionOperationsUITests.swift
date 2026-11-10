import XCTest

@MainActor
final class SessionOperationsUITests: XCTestCase {
    func testAWritableCodexSessionOffersItsQueueLifecycleAndFork() {
        let app = launch(readOnly: false)
        let details = app.collectionViews.firstMatch
        let item = app.staticTexts["Run the release checklist."]
        XCTAssertTrue(item.waitForExistence(timeout: 10), "the inspector lists Codex's queue")
        XCTAssertTrue(app.buttons["Queue item actions"].exists)
        for id in ["queue-add", "queue-start-next", "lifecycle-status", "lifecycle-logs", "lifecycle-stop", "lifecycle-resume", "session-fork"] {
            let button = app.buttons[id]
            for _ in 0..<6 where !(button.exists && button.isHittable) { details.swipeUp() }
            XCTAssertTrue(button.isHittable, id)
            if id == "lifecycle-resume" { attach(app, "writable codex session operations") }
        }
    }

    func testAReadOnlySessionShowsItsQueueButNoWriteActions() {
        let app = launch(readOnly: true)
        let details = app.collectionViews.firstMatch
        XCTAssertTrue(app.staticTexts["Run the release checklist."].waitForExistence(timeout: 10), "a read-only session still lists Codex's queue")
        XCTAssertTrue(app.staticTexts["This session is shared read-only."].exists, "the queue says why it can't be changed")
        attach(app, "read-only codex session operations")
        for _ in 0..<8 {
            for id in ["queue-add", "queue-start-next", "lifecycle-status", "lifecycle-logs", "lifecycle-stop", "lifecycle-resume", "session-fork", "Queue item actions"] {
                XCTAssertFalse(app.buttons[id].exists, id)
            }
            details.swipeUp()
        }
    }

    private func launch(readOnly: Bool) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-inspector", "--preview-codex-ops"] + (readOnly ? ["--preview-read-only"] : [])
        app.launch()
        XCTAssertTrue(app.collectionViews.firstMatch.waitForExistence(timeout: 10))
        return app
    }

    private func attach(_ app: XCUIApplication, _ name: String) {
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }
}
