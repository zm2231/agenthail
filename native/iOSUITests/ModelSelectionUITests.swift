import XCTest

@MainActor
final class ModelSelectionUITests: XCTestCase {
    func testSearchAndApplyUsesRuntimeCatalogRows() {
        let app = launchCreation()
        app.buttons["creation-model-picker"].tap()
        XCTAssertTrue(app.navigationBars["Model"].waitForExistence(timeout: 5))

        let search = app.searchFields.firstMatch
        XCTAssertTrue(search.waitForExistence(timeout: 5))
        search.tap()
        search.typeText("ChatGPT Web")
        XCTAssertTrue(app.staticTexts["ChatGPT Web — Pro"].waitForExistence(timeout: 5))
        capture("Search runtime models", app)
        app.staticTexts["ChatGPT Web — Pro"].tap()
        XCTAssertTrue(app.buttons["Apply"].isHittable)
        app.buttons["Apply"].tap()
        XCTAssertTrue(app.navigationBars["New session"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["creation-model-picker"].label.contains("ChatGPT Web — Pro"))
    }

    func testCustomIDDoesNotApplyUntilExplicitApply() {
        let app = launchCreation()
        app.buttons["creation-model-picker"].tap()
        XCTAssertTrue(app.navigationBars["Model"].waitForExistence(timeout: 5))

        let custom = app.textFields["Enter an explicit model ID"]
        XCTAssertTrue(custom.waitForExistence(timeout: 5))
        custom.tap()
        custom.typeText("provider/custom-model")
        XCTAssertEqual(custom.value as? String, "provider/custom-model")
        app.buttons["Use custom model ID"].tap()
        XCTAssertTrue(app.navigationBars["Model"].exists)
        capture("Custom model staged", app)
        app.buttons["Apply"].tap()
        XCTAssertTrue(app.navigationBars["New session"].waitForExistence(timeout: 5))
        capture("Custom model applied", app)
        XCTAssertTrue(app.buttons["creation-model-picker"].label.contains("provider/custom-model"))
    }

    func testCatalogErrorShowsRetryableState() {
        let app = launchCreation(arguments: ["--preview-models-error"])
        app.buttons["creation-model-picker"].tap()
        XCTAssertTrue(app.staticTexts["Runtime model catalog unavailable"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["Refresh model options"].isHittable)
        app.buttons["Refresh model options"].tap()
        XCTAssertTrue(app.staticTexts["Runtime model catalog unavailable"].waitForExistence(timeout: 5))
    }

    private func launchCreation(arguments: [String] = []) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-new"] + arguments
        app.launch()
        XCTAssertTrue(app.buttons["creation-model-picker"].waitForExistence(timeout: 10))
        return app
    }

    private func capture(_ name: String, _ app: XCUIApplication) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
