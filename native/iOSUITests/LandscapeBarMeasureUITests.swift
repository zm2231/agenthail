import XCTest

@MainActor
final class LandscapeBarMeasureUITests: XCTestCase {
    func testMeasureInPortrait() throws { try measure(landscape: false) }
    func testMeasureInLandscape() throws { try measure(landscape: true) }

    private func measure(landscape: Bool) throws {
        if landscape {
            XCUIDevice.shared.orientation = .landscapeLeft
            addTeardownBlock { @MainActor in XCUIDevice.shared.orientation = .portrait }
        }
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-reading", "--preview-theme", "dark"]
        app.launch()
        let timeline = app.scrollViews["session-timeline"]
        XCTAssertTrue(timeline.waitForExistence(timeout: 10))
        XCTAssertTrue(timeline.waitUntilRowsStill())
        let name = landscape ? "landscape" : "portrait"
        var lines = ["\(name) window \(app.windows.firstMatch.frame)"]
        func frame(_ label: String, _ element: XCUIElement) {
            lines.append("  \(label): \(element.exists ? "\(element.frame.integral)" : "absent")")
        }
        frame("navigation bar", app.navigationBars.firstMatch)
        frame("Working", app.staticTexts["Working"])
        frame("timeline", timeline)
        frame("composer", app.otherElements["composer"])
        frame("composer-input", app.textViews["composer-input"])
        frame("turn-notice", app.descendants(matching: .any)["turn-notice"])
        let tail = app.descendants(matching: .any).matching(NSPredicate(format: "identifier BEGINSWITH 'queue-'"))
        for index in 0..<tail.count {
            let element = tail.element(boundBy: index)
            lines.append("  tail \(element.identifier): \(element.frame.integral)")
        }
        let composerTop = app.otherElements["composer"].exists ? app.otherElements["composer"].frame.minY : timeline.frame.maxY
        let headerBottom = app.staticTexts["Working"].frame.maxY
        lines.append(String(format: "  transcript band between header and composer: %.0f pt of %.0f", composerTop - headerBottom, app.windows.firstMatch.frame.height))

        let probe = tail.count > 0 ? tail.element(boundBy: 0) : timeline.staticTexts.firstMatch
        let before = probe.frame.minY
        let middle = (headerBottom + composerTop) / 2
        let window = app.windows.firstMatch
        let start = window.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: window.frame.midX, dy: middle - 30))
        start.press(forDuration: 0.05, thenDragTo: start.withOffset(CGVector(dx: 0, dy: 80)), withVelocity: 300, thenHoldForDuration: 0.3)
        RunLoop.current.run(until: Date().addingTimeInterval(0.5))
        lines.append(String(format: "  coordinate drag of 80 pt at y=%.0f moved '%@' by %.1f pt", middle - 30, probe.identifier, probe.frame.minY - before))
        let afterDrag = probe.frame.minY
        timeline.swipeDown(velocity: .slow)
        RunLoop.current.run(until: Date().addingTimeInterval(0.8))
        lines.append(String(format: "  timeline.swipeDown moved it by %.1f pt", probe.frame.minY - afterDrag))

        let text = lines.joined(separator: "\n")
        print("BAR-MEASURE\n\(text)")
        let note = XCTAttachment(string: text)
        note.name = "\(name) measure"
        note.lifetime = .keepAlways
        add(note)
        let shot = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        shot.name = "\(name) after scrolling"
        shot.lifetime = .keepAlways
        add(shot)
    }
}
