import XCTest

@MainActor
final class HolderPickerUITests: XCTestCase {
    func testAConversationOpenInTwoTerminalsAsksWhichOneToSendTo() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-holders"]
        app.launch()
        let picker = app.buttons["composer-holder-picker"]
        XCTAssertTrue(picker.waitForExistence(timeout: 10))
        XCTAssertEqual(picker.label, "Open in 2 terminals. Choose one")
        attach(app, "Composer before choosing a terminal")

        picker.tap()
        let second = app.buttons["pid 4102, ttys002, idle"]
        XCTAssertTrue(second.waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["pid 4101, ttys001, busy"].exists)
        second.tap()
        XCTAssertTrue(app.buttons["Sending to pid 4102, ttys002, idle"].waitForExistence(timeout: 5))
        attach(app, "Composer sending to the chosen terminal")
    }

    func testTheSessionListCountsTheTerminalsAConversationIsOpenIn() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-holders"]
        app.launch()
        let count = app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", "2 terminals")).firstMatch
        XCTAssertTrue(count.waitForExistence(timeout: 10))
        attach(app, "Session list with a conversation open in two terminals")
    }

    private func attach(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
