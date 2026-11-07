import XCTest

@MainActor
extension XCTestCase {
    func launchForLiveHosts() -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--live-test-loopback"]
        app.launch()
        return app
    }

    func pairLiveHost(_ index: Int, control: LiveControl, in app: XCUIApplication) throws -> String {
        let field = app.textFields.matching(NSPredicate(format: "placeholderValue == %@", "Or paste a pairing link")).firstMatch
        if !field.waitForExistence(timeout: 10), app.tab("Settings").exists {
            app.tab("Settings").tapWhenReady()
            let add = app.buttons["Add a computer"]
            for _ in 0..<6 where !add.isHittable { app.swipeUp() }
            add.tapWhenReady()
        }
        requireLive(field.waitForExistence(timeout: 10), "the pairing form opens", app)
        let link = try control.pairingLink(host: index)
        let hostID = try XCTUnwrap(URLComponents(string: link)?.queryItems?.first { $0.name == "host" }?.value, link)
        field.tapWhenReady()
        field.typeText(link)
        app.buttons["Connect with pairing link"].tapWhenReady()
        app.buttons["Add computer"].tapWhenReady(timeout: 10)
        let sessions = app.tab("Sessions")
        requireLive(sessions.waitForExistence(timeout: 30), "pairing finishes", app)
        sessions.tapWhenReady()
        app.buttons["scope-all"].tapWhenReady()
        return hostID
    }

    func liveRow(_ title: String, host hostID: String, in app: XCUIApplication) -> XCUIElementQuery {
        app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH %@ AND label CONTAINS %@", "session-\(hostID)::", title))
    }

    func requireLive(_ condition: Bool, _ message: String, _ app: XCUIApplication) {
        guard !condition else { return }
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = message
        shot.lifetime = .keepAlways
        add(shot)
        XCTFail("\(message):\n\(app.debugDescription)")
    }
}
