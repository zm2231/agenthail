import XCTest

@MainActor
final class SessionRowMenuUITests: XCTestCase {
    func testLongPressingARowOffersWhatThatSessionAllows() {
        let app = launch([])
        let menu = open("session-computer::demo", in: app)
        for item in ["Rename…", "Archive", "Add Folder as Project", "Make Project Lead"] {
            XCTAssertTrue(menu.buttons[item].exists, "a working Claude session offers \(item)")
        }
        XCTAssertFalse(menu.buttons["Open in Agenthail"].exists, "a session Agenthail can't open offers no Open in Agenthail")
        XCTAssertFalse(menu.buttons["Move to Another Computer…"].exists, "with no other computer there is nowhere to move to")
        attach(app, "session row menu")
        dismiss(app)

        let lead = open("session-computer::codex-demo", in: app)
        XCTAssertTrue(lead.buttons["Rename…"].exists)
        XCTAssertFalse(lead.buttons["Make Project Lead"].exists, "the project's lead is not offered the lead again")
        dismiss(app)
    }

    func testARowAgenthailCanOpenOffersOpenInAgenthailAndRenameOpensTheNameSheet() {
        let app = launch(["--preview-terminal-question"])
        let menu = open("session-computer::demo", in: app)
        XCTAssertTrue(menu.buttons["Open in Agenthail"].exists)
        menu.buttons["Rename…"].tap()
        XCTAssertTrue(app.navigationBars["Name conversation"].waitForExistence(timeout: 5), "Rename opens the same name sheet as session details")
        app.buttons["Cancel"].tap()
    }

    func testWithAnotherComputerARowOffersMoveAndOpensTheMoveSheet() {
        let app = launch(["--preview-two-computers"])
        let menu = open("session-computer::demo", in: app)
        menu.buttons["Move to Another Computer…"].tap()
        XCTAssertTrue(app.buttons["move-target-vps.example.ts.net"].waitForExistence(timeout: 5), "Move opens the same sheet as session details, listing the other computer")
        app.buttons["Cancel"].tap()
        XCTAssertTrue(waitForGone(app.buttons["move-target-vps.example.ts.net"]))
    }

    private func launch(_ extra: [String]) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app"] + extra
        app.launch()
        return app
    }

    private func open(_ id: String, in app: XCUIApplication) -> XCUIElement {
        let row = app.buttons[id]
        XCTAssertTrue(row.waitForExistence(timeout: 10), id)
        row.press(forDuration: 1.0)
        let menu = app.collectionViews.containing(.button, identifier: "Rename…").firstMatch
        XCTAssertTrue(app.buttons["Rename…"].waitForExistence(timeout: 5), "long-pressing \(id) opens its menu")
        return menu.exists ? menu : app
    }

    private func dismiss(_ app: XCUIApplication) {
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.72)).tap()
        XCTAssertTrue(waitForGone(app.buttons["Rename…"]))
    }

    private func waitForGone(_ element: XCUIElement) -> Bool {
        let gone = expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: element)
        return XCTWaiter().wait(for: [gone], timeout: 5) == .completed
    }

    private func attach(_ app: XCUIApplication, _ name: String) {
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }
}
