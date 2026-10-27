import XCTest

@MainActor
final class UsageMetersUITests: XCTestCase {
    func testEveryAccountSitsUnderTheScopeChipsAndOpensItsDetail() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app"]
        app.launch()
        let usage = app.descendants(matching: .any)["usage-limits"]
        let chips = app.buttons["scope-recent"]
        let firstRow = app.buttons["session-computer::demo"]
        XCTAssertTrue(usage.waitForExistence(timeout: 10))
        XCTAssertTrue(firstRow.waitForExistence(timeout: 5))
        XCTAssertGreaterThanOrEqual(usage.frame.minY, chips.frame.maxY, "usage is below the chips")
        XCTAssertLessThanOrEqual(usage.frame.maxY, firstRow.frame.minY, "usage comes before the sessions")
        XCTAssertLessThanOrEqual(usage.frame.maxX, app.windows.firstMatch.frame.maxX, "the line stays on screen")
        for login in ["demo@example.com: Claude usage 38 percent", "team@example.com", "API key on build-box: Signed in with an API key"] {
            XCTAssertTrue(usage.label.contains(login), "every account reads out, including one without usage: \(usage.label)")
        }
        let line = XCTAttachment(screenshot: app.screenshot())
        line.name = "usage accounts line"
        line.lifetime = .keepAlways
        add(line)
        usage.tap()
        let sheet = app.navigationBars["Accounts"]
        XCTAssertTrue(sheet.waitForExistence(timeout: 5), "tapping usage opens every account's detail")
        XCTAssertTrue(app.staticTexts["team@example.com"].exists)
        let detail = XCTAttachment(screenshot: app.screenshot())
        detail.name = "usage accounts sheet"
        detail.lifetime = .keepAlways
        add(detail)
        let host = app.staticTexts["build-box"]
        for _ in 0..<4 where !host.exists { app.collectionViews.firstMatch.swipeUp() }
        XCTAssertTrue(host.exists, "accounts group by host")
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH 'Signed in with an API key'")).firstMatch.exists, "an API-key root shows its problem in the detail")
        let lower = XCTAttachment(screenshot: app.screenshot())
        lower.name = "usage accounts sheet, other host"
        lower.lifetime = .keepAlways
        add(lower)
        let cooling = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH 'cooling for'")).firstMatch
        for _ in 0..<4 where !cooling.exists { app.collectionViews.firstMatch.swipeUp() }
        XCTAssertTrue(app.staticTexts["ops@example.com"].exists && cooling.exists, "a proxy's accounts show as their own group, with a cooling account's wait")
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS[c] 'shared (proxy)'")).firstMatch.exists, "the group is titled for its proxy")
        let proxy = XCTAttachment(screenshot: app.screenshot())
        proxy.name = "usage accounts sheet, proxy group"
        proxy.lifetime = .keepAlways
        add(proxy)
    }
}
