import UIKit
import XCTest

@MainActor
final class ComposerPasteUITests: XCTestCase {
    func testACopiedImagePastesIntoTheComposerAsAnAttachment() {
        let renderer = UIGraphicsImageRenderer(size: CGSize(width: 64, height: 32))
        UIPasteboard.general.image = renderer.image { context in
            UIColor.systemGreen.setFill()
            context.fill(CGRect(x: 0, y: 0, width: 32, height: 32))
        }
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session"]
        app.launch()
        let composer = app.descendants(matching: .any).matching(identifier: "composer-input").firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: 10))
        composer.tap()
        composer.press(forDuration: 1)
        let paste = app.menuItems["Paste"]
        XCTAssertTrue(paste.waitForExistence(timeout: 5))
        paste.tap()
        XCTAssertTrue(app.descendants(matching: .any)["composer-attachment"].waitForExistence(timeout: 5))
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = "Composer with a pasted image"
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
