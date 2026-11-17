import XCTest

@MainActor
final class ProjectTilesUITests: XCTestCase {
    func testATileCountsItsDecisionsAndOpensItsProjectWithThemFirst() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-tiles"]
        app.launch()
        let api = app.buttons["project-tile-computer::p-api"]
        XCTAssertTrue(api.waitForExistence(timeout: 15))
        XCTAssertTrue(app.buttons["project-tile-computer::p-billing"].exists)
        XCTAssertTrue(app.buttons["project-tile-computer::p-web"].exists)
        XCTAssertFalse(app.buttons["project-tile-computer::p-mobile"].exists, "a phone shows four project tiles")
        let more = app.buttons["project-tile-more"]
        XCTAssertTrue(more.exists)
        XCTAssertEqual(more.label, "2 more projects")
        let counted = expectation(for: NSPredicate(format: "value == '2 waiting'"), evaluatedWith: api)
        wait(for: [counted], timeout: 10)
        XCTAssertLessThan(api.frame.minY, app.buttons["scope-recent"].frame.minY, "the tiles sit above the session chips")
        let row = XCTAttachment(screenshot: app.screenshot())
        row.name = "Project tiles with a decision count"
        row.lifetime = .keepAlways
        add(row)

        api.tap()
        let waiting = app.buttons["decision-computer::demo"]
        XCTAssertTrue(waiting.waitForExistence(timeout: 10))
        XCTAssertTrue(app.buttons["decision-computer::codex-demo"].exists)
        XCTAssertTrue(app.staticTexts["Lead"].exists)
        XCTAssertLessThan(waiting.frame.minY, app.staticTexts["Lead"].frame.minY, "the project's decisions come first")
        let screen = XCTAttachment(screenshot: app.screenshot())
        screen.name = "A project screen with its decisions first"
        screen.lifetime = .keepAlways
        add(screen)
    }

    func testEachTileOpensItsOwnArrangeMenu() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-tiles"]
        app.launch()
        let tiles = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'project-tile-computer::'"))
        XCTAssertTrue(tiles.firstMatch.waitForExistence(timeout: 15))
        let last = tiles.allElementsBoundByIndex.max { $0.frame.minX < $1.frame.minX }!
        let id = last.identifier
        let before = last.frame.minX
        last.press(forDuration: 1.2)
        let earlier = app.buttons["Move Earlier"].firstMatch
        XCTAssertTrue(earlier.waitForExistence(timeout: 5), "the last tile's own menu offers to move it earlier")
        XCTAssertTrue(app.buttons["Pin to Front"].firstMatch.exists)
        let menu = XCTAttachment(screenshot: app.screenshot())
        menu.name = "A tile's arrange menu"
        menu.lifetime = .keepAlways
        add(menu)
        earlier.tap()
        let moved = expectation(for: NSPredicate { _, _ in app.buttons[id].frame.minX < before }, evaluatedWith: nil)
        wait(for: [moved], timeout: 5)
    }
}
