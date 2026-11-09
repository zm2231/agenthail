import XCTest

@MainActor
final class ProjectComputersUITests: XCTestCase {
    func testAProjectOnTwoComputersShowsBothAndEachLead() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-two-computers", "--preview-theme", "dark"]
        app.launch()
        let open = app.buttons["open-project-computer::project-fieldnotes"]
        XCTAssertTrue(open.waitForExistence(timeout: 10))
        open.tapWhenReady()
        XCTAssertTrue(app.staticTexts["2 computers"].waitForExistence(timeout: 5))
        let leads = app.staticTexts["project-leads"]
        for _ in 0..<3 where !leads.exists { app.swipeUp() }
        XCTAssertTrue(leads.waitForExistence(timeout: 5))
        XCTAssertEqual(leads.label, "@release-lead on studio-mac · @server-lead on vps")
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "project on two computers"
        shot.lifetime = .keepAlways
        add(shot)
    }
}
