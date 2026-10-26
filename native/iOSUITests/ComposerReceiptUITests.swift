import XCTest

@MainActor
final class ComposerReceiptUITests: XCTestCase {
    func testTheInstructionReceiptSitsOnTheComposer() {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-delivery", "--preview-theme", "dark"]
        app.launch()
        let receipt = app.buttons["latest-instruction"]
        let composer = app.otherElements["composer"]
        XCTAssertTrue(receipt.waitForExistence(timeout: 10))
        XCTAssertTrue(composer.exists)
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "receipt"
        shot.lifetime = .keepAlways
        add(shot)
        XCTAssertTrue(composer.frame.contains(receipt.frame), "the receipt \(receipt.frame) is outside the composer \(composer.frame)")
    }
}
