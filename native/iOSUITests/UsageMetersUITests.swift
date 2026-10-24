import XCTest

@MainActor
final class UsageMetersUITests: XCTestCase {
    func testUsageSitsDirectlyUnderTheScopeChips() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app"]
        app.launch()
        let usage = app.otherElements["usage-limits"]
        let chips = app.buttons["scope-recent"]
        let firstRow = app.buttons["session-computer::demo"]
        XCTAssertTrue(usage.waitForExistence(timeout: 10))
        XCTAssertTrue(firstRow.waitForExistence(timeout: 5))
        XCTAssertGreaterThanOrEqual(usage.frame.minY, chips.frame.maxY, "usage is below the chips")
        XCTAssertLessThan(usage.frame.minY - chips.frame.maxY, 24, "nothing sits between the chips and usage")
        XCTAssertLessThanOrEqual(usage.frame.maxY, firstRow.frame.minY, "usage comes before the sessions")
        XCTAssertLessThanOrEqual(usage.frame.maxX, app.windows.firstMatch.frame.maxX, "the line stays on screen")
        let spoken = usage.descendants(matching: .any).matching(NSPredicate(format: "label BEGINSWITH 'Claude usage 38 percent'"))
        XCTAssertTrue(spoken.firstMatch.exists, "the in-use meter reads out in full")
        let team = usage.descendants(matching: .any).matching(NSPredicate(format: "label BEGINSWITH 'team@example.com'"))
        XCTAssertTrue(team.firstMatch.exists, "another account near its limit stays on a phone-width line")
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "usage under scope chips"
        shot.lifetime = .keepAlways
        add(shot)
    }
}
