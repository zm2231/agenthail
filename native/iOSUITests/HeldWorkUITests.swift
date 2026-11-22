import XCTest

@MainActor
final class HeldWorkUITests: XCTestCase {
    func testASessionRowShowsTheWorkItHolds() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-held-work", "--preview-theme", ProcessInfo.processInfo.environment["AGENTHAIL_UI_THEME"] ?? "dark"]
        if let size = ProcessInfo.processInfo.environment["AGENTHAIL_UI_CONTENT_SIZE"] {
            app.launchArguments += ["-UIPreferredContentSizeCategoryName", size]
        }
        app.launch()
        app.tab("Sessions").tapWhenReady()
        let holding = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "Holds app-W7 +1: decision: Kept the old token format")).firstMatch
        XCTAssertTrue(holding.waitForExistence(timeout: 15), "the row names the first item it holds, its newest log line and how many more")
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "held work on session rows"
        shot.lifetime = .keepAlways
        add(shot)
        let titled = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "Holds app-W12: Speed up the release build")).firstMatch
        for _ in 0..<4 where !titled.exists { app.swipeUp() }
        XCTAssertTrue(titled.exists, "an item without a log shows its title")
        XCTAssertFalse(app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "Holds app-W")).count > 2, "rows that hold nothing show no line")
    }
}
