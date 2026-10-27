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
        let viaProxy = app.buttons.matching(NSPredicate(format: "label CONTAINS 'Tighten the release notes'")).firstMatch
        for _ in 0..<3 where !viaProxy.isHittable { app.collectionViews.firstMatch.swipeUp() }
        XCTAssertTrue(viaProxy.exists, "the group lists the sessions on roots through its proxy")
        let proxy = XCTAttachment(screenshot: app.screenshot())
        proxy.name = "usage accounts sheet, proxy group"
        proxy.lifetime = .keepAlways
        add(proxy)
    }

    func testAProxyAccountsPreferenceIsChangedOnlyAfterItsPlan() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app"]
        app.launch()
        let usage = app.descendants(matching: .any)["usage-limits"]
        XCTAssertTrue(usage.waitForExistence(timeout: 10))
        usage.tap()
        XCTAssertTrue(app.navigationBars["Accounts"].waitForExistence(timeout: 5))
        let toggles = app.buttons.matching(identifier: "proxy-prefer-toggle")
        for _ in 0..<6 where toggles.count < 3 || !toggles.element(boundBy: 1).isHittable { app.collectionViews.firstMatch.swipeUp() }
        XCTAssertEqual(toggles.count, 3, "every proxy account has the toggle")
        XCTAssertEqual(toggles.element(boundBy: 0).label, "Preferred", "a preferred account says so")
        XCTAssertEqual(toggles.element(boundBy: 1).label, "Prefer")
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH 'set on build-box at '")).firstMatch.exists, "a value Agenthail wrote is credited")
        let rows = XCTAttachment(screenshot: app.screenshot())
        rows.name = "proxy accounts with preference actions"
        rows.lifetime = .keepAlways
        add(rows)
        toggles.element(boundBy: 1).tap()
        XCTAssertTrue(app.staticTexts["lead@example.com: not preferred → preferred"].waitForExistence(timeout: 5), "the plan shows the before and after")
        XCTAssertTrue(app.staticTexts["Other accounts are not changed."].exists)
        XCTAssertTrue(app.staticTexts["Affects"].exists, "the hosts and sessions come under Affects")
        let confirm = app.buttons["proxy-prefer-confirm"]
        XCTAssertEqual(confirm.label, "Prefer lead@example.com", "the button is named for the effect")
        let plan = XCTAttachment(screenshot: app.screenshot())
        plan.name = "proxy preference plan"
        plan.lifetime = .keepAlways
        add(plan)
        confirm.tap()
        XCTAssertTrue(app.staticTexts["proxy-prefer-problem"].waitForExistence(timeout: 5), "a refused write is shown, not swallowed")
        app.buttons["Cancel"].tap()
        let only = app.buttons.matching(identifier: "proxy-prefer-only")
        XCTAssertEqual(only.count, 3, "prefer only is offered where the provider has other accounts")
        only.element(boundBy: 1).tap()
        XCTAssertTrue(app.staticTexts["ops@example.com: preferred → not preferred"].waitForExistence(timeout: 5), "prefer only names every account it turns off")
        XCTAssertEqual(app.switches.firstMatch.value as? String, "0", "moving running sessions starts off")
        XCTAssertEqual(app.buttons["proxy-prefer-confirm"].label, "Prefer only lead@example.com (turns off ops@example.com)")
        let exclusive = XCTAttachment(screenshot: app.screenshot())
        exclusive.name = "proxy prefer only plan"
        exclusive.lifetime = .keepAlways
        add(exclusive)
    }
}
