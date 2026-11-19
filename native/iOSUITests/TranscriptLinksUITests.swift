import XCTest

@MainActor
final class TranscriptLinksUITests: XCTestCase {
    func testAnAddressInACodeSpanOpensWhenTapped() {
        for theme in ["dark", "light"] {
            let app = XCUIApplication()
            app.launchArguments = ["--preview-session", "--preview-links", "--preview-theme", theme]
            app.launch()
            let reply = app.staticTexts.containing(NSPredicate(format: "label CONTAINS 'management.html'")).firstMatch
            XCTAssertTrue(reply.waitForExistence(timeout: 10), "\(theme): the reply with the addresses is shown")
            attach("\(theme) links")
            let link = app.links.matching(NSPredicate(format: "label CONTAINS 'management.html'")).firstMatch
            XCTAssertTrue(link.waitForExistence(timeout: 5), "\(theme): the address in the code span is a link: \(app.links.allElementsBoundByIndex.map(\.label))")
            XCTAssertTrue(app.links.matching(NSPredicate(format: "label CONTAINS 'status.html'")).firstMatch.exists, "\(theme): the bare address is a link")
            XCTAssertTrue(app.links.matching(NSPredicate(format: "label CONTAINS 'example.com/status'")).firstMatch.exists, "\(theme): the address in the person's message is a link")
            guard link.exists else { continue }
            link.coordinate(withNormalizedOffset: CGVector(dx: 0, dy: 1)).withOffset(CGVector(dx: 24, dy: -10)).tap()
            let safari = XCUIApplication(bundleIdentifier: "com.apple.mobilesafari")
            XCTAssertTrue(safari.wait(for: .runningForeground, timeout: 10), "\(theme): tapping the link opens it")
            safari.terminate()
            app.activate()
            XCTAssertTrue(app.wait(for: .runningForeground, timeout: 10))
            XCTAssertFalse(app.menuItems["Copy"].waitForExistence(timeout: 1) || app.buttons["Copy"].exists, "\(theme): tapping a link opens it without starting a selection")
            let own = app.links.matching(NSPredicate(format: "label CONTAINS 'example.com/status'")).firstMatch
            XCTAssertTrue(own.waitForExistence(timeout: 5), "\(theme): the person's link is still there")
            own.coordinate(withNormalizedOffset: CGVector(dx: 0, dy: 1)).withOffset(CGVector(dx: 24, dy: -10)).tap()
            XCTAssertTrue(safari.wait(for: .runningForeground, timeout: 10), "\(theme): tapping the person's link opens it")
            safari.terminate()
            app.activate()
            let agent = app.buttons["agent-message-links-agent"]
            XCTAssertTrue(agent.waitForExistence(timeout: 5), "\(theme): the short agent message is shown")
            XCTAssertEqual(agent.value as? String, "Collapsed", "\(theme): a short agent message with an address can be opened")
            agent.tap()
            let staging = app.links.matching(NSPredicate(format: "label CONTAINS 'example.com/staging'")).firstMatch
            XCTAssertTrue(staging.waitForExistence(timeout: 5), "\(theme): the opened agent message links its address")
            staging.coordinate(withNormalizedOffset: CGVector(dx: 0, dy: 1)).withOffset(CGVector(dx: 24, dy: -8)).tap()
            XCTAssertTrue(safari.wait(for: .runningForeground, timeout: 10), "\(theme): tapping the agent's link opens it")
            safari.terminate()
            app.terminate()
        }
    }

    private func attach(_ name: String) {
        let shot = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }
}
