import XCTest

@MainActor
final class CompactSettingsUITests: XCTestCase {
    func testSessionDetailsOfferAutoCompactAndWhereTheWindowApplies() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-theme", "dark"]
        app.launch()
        let menu = app.buttons["Session menu"]
        XCTAssertTrue(menu.waitForExistence(timeout: 10))
        menu.tapWhenReady()
        app.buttons["Session details"].tapWhenReady()
        XCTAssertTrue(app.navigationBars["Session details"].waitForExistence(timeout: 5))
        let auto = app.buttons["compact-setting-autoCompact"]
        let list = app.collectionViews.firstMatch
        for _ in 0..<6 where !auto.isHittable { list.swipeUp() }
        XCTAssertTrue(auto.isHittable, "session details offer auto-compact")
        XCTAssertTrue(app.buttons["compact-setting-compactWindow"].exists, "session details offer the compact window")
        XCTAssertTrue(app.staticTexts["For new sessions on default"].exists, "the window says it applies to new sessions on its root")
    }
}
