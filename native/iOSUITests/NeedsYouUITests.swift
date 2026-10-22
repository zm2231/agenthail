import XCTest

@MainActor
final class NeedsYouUITests: XCTestCase {
    func testNeedsYouListsWaitingThenIncomingThenUndeliveredWithReasons() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-waiting", "--preview-attention"]
        app.launch()
        let asking = app.buttons["needs-you-computer::demo"]
        let incoming = app.buttons["needs-you-computer::codex-demo"]
        let undelivered = app.buttons["needs-you-computer::saved-demo"]
        XCTAssertTrue(asking.waitForExistence(timeout: 10))
        XCTAssertTrue(incoming.exists)
        XCTAssertTrue(undelivered.exists)
        XCTAssertTrue(asking.label.contains("Asks you a question"), asking.label)
        XCTAssertTrue(incoming.label.contains("Delivery outcome could not be confirmed"), incoming.label)
        XCTAssertTrue(undelivered.label.contains("not delivered"), undelivered.label)
        XCTAssertLessThan(asking.frame.minY, incoming.frame.minY)
        XCTAssertLessThan(incoming.frame.minY, undelivered.frame.minY)
        XCTAssertFalse(app.buttons["needs-you-h0123456789ab::remote-demo"].exists)
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = "Needs you with reasons"
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
