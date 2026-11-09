import XCTest

@MainActor
final class SessionNameUITests: XCTestCase {
    func testClearingALeadsNameOpensTheSheetAndSaysWhy() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-inspector", "--preview-named", "--preview-lead-name", "--preview-theme", "dark"]
        app.launch()
        let name = app.buttons["Name conversation"]
        for _ in 0..<6 where !name.isHittable { app.swipeUp() }
        Thread.sleep(forTimeInterval: 1)
        name.tapWhenReady()
        let field = app.textViews.firstMatch.exists ? app.textViews.firstMatch : app.textFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 5), "the name sheet opens")
        XCTAssertEqual(field.value as? String, "builder")
        field.coordinate(withNormalizedOffset: CGVector(dx: 0.9, dy: 0.3)).tap()
        field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: 12))
        let clear = app.buttons["Clear Name"]
        XCTAssertTrue(clear.waitForExistence(timeout: 5), "an emptied name offers Clear Name")
        clear.tap()
        let refusal = app.staticTexts.containing(NSPredicate(format: "label BEGINSWITH %@", "@builder is this project's lead")).firstMatch
        XCTAssertTrue(refusal.waitForExistence(timeout: 5), "a lead's name says where to change it")
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "lead refusal"
        shot.lifetime = .keepAlways
        add(shot)
    }
}
