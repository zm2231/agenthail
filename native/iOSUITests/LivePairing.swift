import XCTest

@MainActor
extension XCTestCase {
    func launchForLiveHosts(_ extra: [String] = []) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--live-test-loopback"] + extra
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

    // The sessions list is lazy, so a row below the fold is not in the tree until the list scrolls to it.
    func reveal(_ row: XCUIElement, in app: XCUIApplication, timeout: TimeInterval) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        var upward = true
        var swipes = 0
        while !row.waitForExistence(timeout: min(1, max(0, deadline.timeIntervalSinceNow))) {
            guard Date() < deadline else { return false }
            let list = app.collectionViews.firstMatch
            let target = list.exists ? list : app
            if upward { target.swipeUp() } else { target.swipeDown() }
            swipes += 1
            if swipes % 6 == 0 { upward.toggle() }
        }
        return true
    }

    func reveal(_ row: XCUIElement, in app: XCUIApplication, value: String, matching: Bool = true, timeout: TimeInterval) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        repeat {
            if reveal(row, in: app, timeout: max(0, deadline.timeIntervalSinceNow)), ((row.value as? String) == value) == matching { return true }
            Thread.sleep(forTimeInterval: min(0.5, max(0, deadline.timeIntervalSinceNow)))
        } while Date() < deadline
        return false
    }

    func showAllSessions(in app: XCUIApplication) {
        app.tab("Sessions").tapWhenReady(timeout: 20)
        let scopeAll = app.buttons["scope-all"]
        for _ in 0..<12 where !scopeAll.isHittable {
            let list = app.collectionViews.firstMatch
            (list.exists ? list : app).swipeDown()
        }
        scopeAll.tapWhenReady(timeout: 20)
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
