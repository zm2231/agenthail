import XCTest

@MainActor
final class NeedsYouUITests: XCTestCase {
    func testAPromptAgenthailCannotReachAndAUsageLimitSayWhatToDo() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-unreachable"]
        app.launch()
        let stranded = app.buttons["needs-you-computer::demo"]
        let limited = app.buttons["needs-you-h0123456789ab::remote-demo-2"]
        XCTAssertTrue(stranded.waitForExistence(timeout: 10))
        XCTAssertTrue(limited.exists)
        let header = app.descendants(matching: .any)["needs-you-header"]
        XCTAssertTrue(header.exists)
        XCTAssertEqual(header.label, "Needs you", "the Inbox badge is the one Needs you count")
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
