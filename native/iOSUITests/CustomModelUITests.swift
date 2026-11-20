import XCTest

@MainActor
final class CustomModelUITests: XCTestCase {
    func testAChosenCustomModelIDShowsOnceInTheModelSheet() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-theme", "dark"]
        app.launch()
        let summary = app.buttons["session-summary"]
        XCTAssertTrue(summary.waitForExistence(timeout: 10))
        summary.tapWhenReady()
        XCTAssertTrue(app.navigationBars["Session details"].waitForExistence(timeout: 5))
        let change = app.buttons["Change model"]
        let list = app.collectionViews.firstMatch
        for _ in 0..<6 where !change.isHittable { list.swipeUp() }
        XCTAssertTrue(change.isHittable, "session details change the model")
        change.tap()
        XCTAssertTrue(app.navigationBars["Model"].waitForExistence(timeout: 10))
        let field = app.textFields["Enter an explicit model ID"]
        let search = app.searchFields["Search model options"]
        let clearOfSearch = { field.isHittable && (!search.exists || !field.frame.intersects(search.frame.insetBy(dx: 0, dy: -8))) }
        for _ in 0..<8 where !clearOfSearch() { app.collectionViews.firstMatch.swipeUp() }
        XCTAssertTrue(clearOfSearch(), "the custom model field is reachable clear of the search field, at \(field.frame) with search at \(search.frame)")

        let id = "provider/my-model"
        field.tap()
        field.typeText(id)
        app.buttons["Use custom model ID"].tap()

        XCTAssertTrue(app.images["custom-model-selected"].waitForExistence(timeout: 5), "the chosen custom ID is marked in its field")
        XCTAssertEqual(field.value as? String, id)
        XCTAssertEqual(app.staticTexts.matching(NSPredicate(format: "label == %@", id)).count, 0, "the chosen ID is not repeated below its field")
        XCTAssertFalse(app.buttons["Use custom model ID"].isEnabled, "the chosen ID cannot be chosen again")

        field.tap()
        field.typeText("-2")
        XCTAssertFalse(app.images["custom-model-selected"].exists, "an edited ID is not chosen until it is used")
        XCTAssertTrue(app.buttons["Use custom model ID"].isEnabled)
    }
}
