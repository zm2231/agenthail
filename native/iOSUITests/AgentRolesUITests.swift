import XCTest

@MainActor
final class AgentRolesUITests: XCTestCase {
    private func launch(_ theme: String) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-roles", "--preview-theme", theme]
        app.launch()
        app.tab("Settings").tapWhenReady()
        return app
    }

    private func open(_ app: XCUIApplication, _ id: String) {
        let link = app.buttons[id]
        for _ in 0..<6 where !link.isHittable { app.swipeUp() }
        link.tapWhenReady()
    }

    private func shoot(_ app: XCUIApplication, _ name: String) {
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }

    func testEditingADecisionMarksItChangedAndSaves() {
        let app = launch("dark")
        open(app, "agent-roles")
        app.buttons["agent-role-reviewer"].tapWhenReady()
        let decision = app.buttons["agent-decision-session-create-remote"]
        XCTAssertTrue(decision.waitForExistence(timeout: 5))
        shoot(app, "role detail")
        decision.tapWhenReady()
        app.buttons["Ask"].tapWhenReady()
        XCTAssertTrue(app.staticTexts["Changed"].waitForExistence(timeout: 5), "a changed decision is marked")
        let save = app.buttons["agent-role-save"]
        XCTAssertTrue(save.isEnabled)
        save.tap()
        let saved = NSPredicate(format: "isEnabled == false")
        expectation(for: saved, evaluatedWith: save)
        waitForExpectations(timeout: 5)
        XCTAssertTrue(decision.label.contains("Ask"), decision.label)
        shoot(app, "decision saved")
    }

    func testAddingAnAssignmentListsIt() {
        let app = launch("light")
        open(app, "agent-assignments")
        XCTAssertTrue(app.staticTexts["Applies to no session: no such session"].waitForExistence(timeout: 5), "a stale assignment says why")
        shoot(app, "assignments")
        app.buttons["agent-assign-open"].tapWhenReady()
        app.buttons["Identity"].tapWhenReady()
        let target = app.textFields["agent-assign-target"]
        target.tapWhenReady()
        target.typeText("release-bot")
        app.buttons["agent-assign-role"].tapWhenReady()
        app.buttons["lead"].tapWhenReady()
        shoot(app, "assign sheet")
        app.buttons["agent-assign"].tapWhenReady()
        XCTAssertTrue(app.descendants(matching: .any)["agent-assignment-release-bot"].waitForExistence(timeout: 5), "the new assignment is listed")
        shoot(app, "assignment added")
    }
}
