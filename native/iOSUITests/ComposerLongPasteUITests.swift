import XCTest

@MainActor
final class ComposerLongPasteUITests: XCTestCase {
    override func tearDown() {
        XCUIDevice.shared.orientation = .portrait
        super.tearDown()
    }

    func testALongPasteIsOneTokenThatOpensForEditing() {
        let pasted = (1 ... 15).map { "\($0). A step of the release plan that someone pasted in" }.joined(separator: "\n")
        for orientation in [UIDeviceOrientation.portrait, .landscapeLeft] {
            for theme in ["dark", "light"] {
                let shape = orientation == .portrait ? "narrow" : "wide"
                let name = "\(theme) \(shape)"
                XCUIDevice.shared.orientation = orientation
                let app = XCUIApplication()
                app.launchArguments = ["--preview-session", "--preview-theme", theme]
                app.launch()
                let input = app.textViews["composer-input"]
                XCTAssertTrue(input.waitForExistence(timeout: 10))
                for _ in 0 ..< 3 where !app.keyboards.firstMatch.exists {
                    input.tapWhenReady(timeout: 10)
                    _ = app.keyboards.firstMatch.waitForExistence(timeout: 3)
                }
                XCTAssertTrue(app.keyboards.firstMatch.exists, "\(name): the composer takes the keyboard")
                input.press(forDuration: 1.2)
                menu(app, "Select All").tap()
                input.typeText(XCUIKeyboardKey.delete.rawValue)
                XCTAssertEqual(input.value as? String ?? "", "", "\(name): the preview draft is cleared")
                UIPasteboard.general.string = pasted
                input.press(forDuration: 1.2)
                let paste = menu(app, "Paste")
                XCTAssertTrue(paste.waitForExistence(timeout: 5), "\(name): the edit menu offers Paste")
                paste.tap()
                XCTAssertTrue(until { !(input.value as? String ?? "").isEmpty }, "\(name): the paste lands in the composer")
                if !until(2, { focused(input) }) {
                    input.coordinate(withNormalizedOffset: CGVector(dx: 0.98, dy: 0.5)).tap()
                }
                XCTAssertTrue(until { focused(input) }, "\(name): the composer keeps the keyboard after the paste")
                app.typeText("Would this be useful for us?")
                let screen = app.windows.firstMatch.frame
                XCTAssertLessThan(input.frame.height, screen.height * 0.4, "\(name): the paste stays one token, not fifteen lines")
                XCTAssertTrue((input.value as? String ?? "").hasSuffix("Would this be useful for us?"), "\(name): the question follows the paste")
                attach(app, "\(name) token")
                input.coordinate(withNormalizedOffset: CGVector(dx: 0, dy: 0)).withOffset(CGVector(dx: 40, dy: 10)).tap()
                let editor = app.descendants(matching: .any)["paste-token-editor"]
                XCTAssertTrue(editor.waitForExistence(timeout: 5), "\(name): tapping the token opens its text")
                XCTAssertTrue(app.navigationBars["Pasted text · 15 lines"].exists, "\(name): the editor names the paste")
                attach(app, "\(name) editor")
                app.buttons["Done"].tap()
                app.terminate()
            }
        }
    }

    private func focused(_ element: XCUIElement) -> Bool {
        element.value(forKey: "hasKeyboardFocus") as? Bool == true
    }

    private func until(_ timeout: TimeInterval = 5, _ condition: () -> Bool) -> Bool {
        let end = Date().addingTimeInterval(timeout)
        while Date() < end {
            if condition() { return true }
            RunLoop.current.run(until: Date().addingTimeInterval(0.1))
        }
        return condition()
    }

    private func menu(_ app: XCUIApplication, _ title: String) -> XCUIElement {
        let item = app.menuItems[title]
        return item.waitForExistence(timeout: 3) ? item : app.buttons[title]
    }

    private func attach(_ app: XCUIApplication, _ name: String) {
        let shot = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }
}
