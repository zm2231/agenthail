import XCTest

@MainActor
final class HostGroupsUITests: XCTestCase {
    func testAChannelIsMessagedFromItsComputersSettingsAndSaysWhoGotIt() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-app", "--preview-groups", "--preview-theme", "dark"]
        app.launch()
        app.tab("Settings").tapWhenReady()
        let groups = app.buttons["host-groups-computer"]
        for _ in 0..<4 where !groups.isHittable { app.swipeUp() }
        groups.tapWhenReady()
        let field = app.textFields["channel-message-release"].exists ? app.textFields["channel-message-release"] : app.textViews["channel-message-release"]
        XCTAssertTrue(field.waitForExistence(timeout: 5))
        field.tap()
        field.typeText("Freeze the release branch.")
        app.buttons["channel-send-release"].tapWhenReady()
        let sent = app.staticTexts["channel-sent-release"]
        XCTAssertTrue(sent.waitForExistence(timeout: 5))
        XCTAssertTrue(sent.label.hasPrefix("Sent to 2:"), sent.label)
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "channel message sent"
        shot.lifetime = .keepAlways
        add(shot)
    }
}
