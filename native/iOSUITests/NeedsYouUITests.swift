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

    func testAPromptAgenthailCannotReachAndAUsageLimitSayWhatToDo() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-unreachable"]
        app.launch()
        let stranded = app.buttons["needs-you-demo"]
        let limited = app.buttons["needs-you-h0123456789ab::remote-demo-2"]
        XCTAssertTrue(stranded.waitForExistence(timeout: 10))
        XCTAssertTrue(limited.exists)
        XCTAssertTrue(stranded.label.contains("Waiting on a prompt Agenthail can't reach. Stop and resume to clear it"), stranded.label)
        XCTAssertTrue(limited.label.contains("Usage limit, resets"), limited.label)
        XCTAssertLessThan(stranded.frame.minY, limited.frame.minY)
        let list = XCTAttachment(screenshot: app.screenshot())
        list.name = "Needs you with an unreachable prompt and a usage limit"
        list.lifetime = .keepAlways
        add(list)

        stranded.tap()
        let card = app.otherElements["waiting-elsewhere-preview-approval"]
        XCTAssertTrue(card.waitForExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["Claude Code is waiting on a prompt Agenthail can't reach"].exists || app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "can't reach")).firstMatch.exists)
        XCTAssertTrue(app.buttons["Stop and resume"].exists)
        let shown = XCTAttachment(screenshot: app.screenshot())
        shown.name = "The card for a prompt Agenthail cannot reach"
        shown.lifetime = .keepAlways
        add(shown)
    }
}
