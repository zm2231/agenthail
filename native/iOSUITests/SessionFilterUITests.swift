import XCTest

@MainActor
final class SessionFilterUITests: XCTestCase {
    func testAStatusFilterNarrowsTheListAndEveryScopeShowsItsCount() {
        let app = XCUIApplication()
        let theme = ProcessInfo.processInfo.environment["SHOT_THEME"] ?? "dark"
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-theme", theme]
        app.launch()
        let working = app.buttons["session-computer::demo"]
        let idle = app.buttons["session-computer::codex-demo"]
        XCTAssertTrue(working.waitForExistence(timeout: 10))
        XCTAssertTrue(idle.exists)
        for scope in ["Running", "Recent", "All", "Archived"] {
            let chip = app.buttons["scope-" + scope.lowercased()]
            XCTAssertNotNil(chip.label.range(of: "^\(scope), [0-9]+$", options: .regularExpression), "every scope chip says its count: \(chip.label)")
        }

        choose("Working", in: app)
        let chip = app.buttons["filter-chip-status"]
        XCTAssertTrue(chip.waitForExistence(timeout: 5))
        XCTAssertEqual(chip.label, "Remove filter: Working")
        XCTAssertTrue(working.waitForExistence(timeout: 5))
        XCTAssertTrue(waitForGone(idle), "an idle session stayed listed under the Working filter")
        XCTAssertEqual(app.buttons["scope-recent"].label, "Recent, 2", "the chip counts follow the filter")
        attach(app, "filter working \(theme)")

        working.tapWhenReady()
        app.backToSessions()
        XCTAssertTrue(app.navigationBars["Sessions"].waitForExistence(timeout: 5))
        XCTAssertTrue(chip.waitForExistence(timeout: 5), "the filter survives opening and closing a session")
        XCTAssertLessThan(chip.frame.width, 200, "the chip is its own element, not the whole row: \(chip.frame)")
        chip.tapWhenStill()
        XCTAssertTrue(idle.waitForExistence(timeout: 5), "removing the chip brings the idle session back")
        XCTAssertFalse(chip.exists)

        app.buttons["scope-all"].tapWhenReady()
        choose("Stopped", in: app)
        let empty = app.staticTexts["No sessions match these filters."]
        XCTAssertTrue(empty.waitForExistence(timeout: 5))
        attach(app, "filter empty \(theme)")
        app.buttons["clear-filters"].tapWhenReady()
        XCTAssertTrue(idle.waitForExistence(timeout: 5), "Clear Filters brings the list back")
        XCTAssertTrue(app.buttons["scope-all"].isSelected, "the scope stays when filters are cleared")
    }

    private func choose(_ status: String, in app: XCUIApplication) {
        app.buttons["filter-menu"].tapWhenReady()
        let option = app.buttons[status]
        XCTAssertTrue(option.waitForExistence(timeout: 5), "the filter menu offers \(status)")
        attach(app, "filter menu")
        option.tap()
    }

    private func waitForGone(_ element: XCUIElement) -> Bool {
        let gone = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: element)
        return XCTWaiter.wait(for: [gone], timeout: 5) == .completed
    }

    private func attach(_ app: XCUIApplication, _ name: String) {
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }
}
