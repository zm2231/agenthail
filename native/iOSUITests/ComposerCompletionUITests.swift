import XCTest

@MainActor
final class ComposerCompletionUITests: XCTestCase {
    private func shot(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }

    func testSuggestionsCompleteCommandsAndFilesAboveTheKeyboard() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session"]
        app.launch()
        let composer = app.textViews["composer-input"]
        XCTAssertTrue(composer.waitForExistence(timeout: 10))
        composer.coordinate(withNormalizedOffset: CGVector(dx: 0.98, dy: 0.9)).tap()
        composer.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: ((composer.value as? String) ?? "").count + 2))
        XCTAssertEqual(composer.value as? String, "")
        composer.typeText("/")
        let suggestions = app.descendants(matching: .any)["composer-suggestions"]
        XCTAssertTrue(suggestions.waitForExistence(timeout: 5))
        let review = suggestions.buttons["/review"]
        XCTAssertTrue(review.waitForExistence(timeout: 5))
        shot(app, "Command suggestions")
        review.tap()
        XCTAssertEqual(composer.value as? String, "/review ")

        composer.typeText("look at @Note")
        let file = suggestions.buttons["Sources/NoteStore.swift"]
        XCTAssertTrue(file.waitForExistence(timeout: 5))
        XCTAssertTrue(suggestions.buttons["Sources/Notebook.swift"].exists)
        shot(app, "File suggestions")
        file.tap()
        XCTAssertEqual(composer.value as? String, "/review look at @Sources/NoteStore.swift ")
        XCTAssertFalse(suggestions.exists)

        composer.typeText("$")
        XCTAssertTrue(suggestions.buttons["$fieldnotes"].waitForExistence(timeout: 5))
        suggestions.buttons["Hide suggestions"].tap()
        XCTAssertFalse(suggestions.waitForExistence(timeout: 1))
    }
}
