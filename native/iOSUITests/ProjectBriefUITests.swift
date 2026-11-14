import XCTest

@MainActor
final class ProjectBriefUITests: XCTestCase {
    func testTheLeadIsAskedToCatchThePersonUpFromTheProjectScreen() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-theme", "dark"]
        app.launch()
        let open = app.buttons["open-project-computer::project-fieldnotes"]
        XCTAssertTrue(open.waitForExistence(timeout: 10))
        open.tapWhenReady()
        let brief = app.buttons["project-brief"]
        for _ in 0..<3 where !brief.isHittable { app.swipeUp() }
        XCTAssertEqual(brief.label, "Catch me up")
        brief.tapWhenReady()
        let notice = app.staticTexts["project-brief-notice"]
        XCTAssertTrue(notice.waitForExistence(timeout: 5))
        XCTAssertEqual(notice.label, "Asked @release-lead to catch you up on the last 24 hours. It replies after its current turn.")
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "project brief asked"
        shot.lifetime = .keepAlways
        add(shot)
    }
}
