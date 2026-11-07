import XCTest

@MainActor
final class LiveHostUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    func testPairingWithALiveHostListsItsConversations() throws {
        let control = try LiveControl()
        let app = XCUIApplication()
        app.launchArguments = ["--live-test-loopback"]
        app.launch()

        let field = app.textFields.matching(NSPredicate(format: "placeholderValue == %@", "Or paste a pairing link")).firstMatch
        if !field.waitForExistence(timeout: 10), app.tab("Settings").exists {
            app.tab("Settings").tapWhenReady()
            let add = app.buttons["Add a computer"]
            for _ in 0..<6 where !add.isHittable { app.swipeUp() }
            add.tapWhenReady()
        }
        require(field.waitForExistence(timeout: 10), "the pairing form opens", app)
        field.tapWhenReady()
        let link = try control.pairingLink(host: 0)
        let hostID = try XCTUnwrap(URLComponents(string: link)?.queryItems?.first { $0.name == "host" }?.value, link)
        field.typeText(link)
        app.buttons["Connect with pairing link"].tapWhenReady()
        app.buttons["Add computer"].tapWhenReady(timeout: 10)

        let sessions = app.tab("Sessions")
        require(sessions.waitForExistence(timeout: 30), "pairing finishes", app)
        sessions.tapWhenReady()
        app.buttons["scope-all"].tapWhenReady()
        for title in ["Check the release notes", "Tidy the build script"] {
            let rows = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH %@ AND label CONTAINS %@", "session-\(hostID)::", title))
            require(rows.firstMatch.waitForExistence(timeout: 30), "\(title) is listed from Live A", app)
            XCTAssertEqual(rows.count, 1, "Live A lists \(title) once")
        }
    }

    private func require(_ condition: Bool, _ message: String, _ app: XCUIApplication) {
        guard !condition else { return }
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = message
        shot.lifetime = .keepAlways
        add(shot)
        XCTFail("\(message):\n\(app.debugDescription)")
    }
}
