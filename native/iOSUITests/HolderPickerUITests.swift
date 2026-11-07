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

    private func attach(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
